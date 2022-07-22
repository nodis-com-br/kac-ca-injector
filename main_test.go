package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sRuntime "k8s.io/apimachinery/pkg/runtime"
)

var (
	request         *http.Request
	response        *http.Response
	annotations     map[string]string
	rawObject       []byte
	admissionReview []byte
	caBundleUrl     string
)

var (
	handlerFuncs     = []HandleFunc{handleValidate, handleMutate}
	target           = "/"
	namespace        = "default"
	defaultMediaType = "application/json"
	invalidMediaType = "text/html"
	invalidNamespace = "invalid"
	invalidResource  = corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
	}
	invalidResourceGVR = metav1.GroupVersionResource{
		Version:  "v1",
		Resource: "ConfigMaps",
	}
)

func init() {
	_ = os.Setenv(keyDebug, "true")
	_ = os.Setenv(keyKubeconfig, "./kubeconfig")
	_ = os.Setenv(keyConfigMapName, "ca-bundle")
	_ = os.Setenv(keyCABundleFilename, "ca_bundle.pem")
	_ = os.Setenv(keyCABundleAnnotation, "example.com/ca-injector")
	_ = os.Setenv(keyCABundleURL, "https://curl.se/ca/cacert.pem")
	_ = os.Setenv(keyPodNamespace, "botland")
	caBundleUrl = os.Getenv(keyCABundleURL)
	annotations = map[string]string{os.Getenv(keyCABundleAnnotation): "true"}
}

// Helper functions

func getFuncName(temp interface{}) string {
	strs := strings.Split(runtime.FuncForPC(reflect.ValueOf(temp).Pointer()).Name(), ".")
	return strs[len(strs)-1]
}

func invalidStatusCode(t *testing.T, expected int, actual int) bool {
	if actual != expected {
		t.Errorf("expected status %d got %d", expected, actual)
		return true
	}
	return false
}

func invalidResponseBody(t *testing.T, body io.Reader) bool {
	var admissionReviewResponse admissionv1.AdmissionReview
	var operations []jsonpatch.Patch
	encodedBody, _ := ioutil.ReadAll(body)
	_ = json.Unmarshal(encodedBody, &admissionReviewResponse)
	_ = json.Unmarshal(admissionReviewResponse.Response.Patch, &operations)
	if len(operations) == 0 {
		t.Errorf("patch operations array is empty")
		return true
	}
	return false
}

func deleteConfigMap(namespace string) {
	clientSet, _ := getKubernetesClientSet()
	_ = clientSet.CoreV1().ConfigMaps(namespace).Delete(context.Background(), os.Getenv(keyConfigMapName), metav1.DeleteOptions{})
}

func podFactory(namespace string, labels map[string]string, annotations map[string]string, volumeCount int) ([]byte, error) {
	podDefinition := corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-pod",
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{},
			Containers: []corev1.Container{
				{
					VolumeMounts: []corev1.VolumeMount{},
				},
			},
		},
	}
	for i := 1; i <= volumeCount; i++ {
		podDefinition.Spec.Volumes = append(podDefinition.Spec.Volumes, corev1.Volume{
			Name: fmt.Sprintf("volume_%d", i),
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
		podDefinition.Spec.Containers[0].VolumeMounts = append(podDefinition.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
			Name:      fmt.Sprintf("volume_%d", i),
			MountPath: fmt.Sprintf("path_%d", i),
		})
	}
	return json.Marshal(podDefinition)
}

func admissionReviewFactory(gvr metav1.GroupVersionResource, rawObject []byte) ([]byte, error) {
	return json.Marshal(admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Request: &admissionv1.AdmissionRequest{
			Resource: gvr,
			Object: k8sRuntime.RawExtension{
				Raw: rawObject,
			},
		},
	})
}

func testRequest(method string, handler HandleFunc, rawBody string, mediaType string) *http.Response {
	request = httptest.NewRequest(method, target, strings.NewReader(rawBody))
	request.Header.Set("Content-Type", mediaType)
	return generateResponse(request, handler)
}

func generateResponse(req *http.Request, handlerFunc HandleFunc) *http.Response {
	w := httptest.NewRecorder()
	handlerFunc(w, req)
	response = w.Result()
	defer func() { _ = response.Body.Close() }()
	return response
}

// Request validation tests

func TestForbiddenMethods(t *testing.T) {
	forbiddenMethods := []string{"GET", "PUT", "DELETE"}
	for _, handler := range handlerFuncs {
		for _, method := range forbiddenMethods {
			t.Logf("testing func %s, forbidden method %s", getFuncName(handler), method)
			response = testRequest(method, handler, "", defaultMediaType)
			_ = invalidStatusCode(t, http.StatusMethodNotAllowed, response.StatusCode)
		}
	}
}

func TestInvalidMediaType(t *testing.T) {
	for _, handler := range handlerFuncs {
		t.Logf("testing func %s, invalid media type", getFuncName(handler))
		request = httptest.NewRequest(http.MethodPost, target, nil)
		response = testRequest(http.MethodPost, handler, "", invalidMediaType)
		_ = invalidStatusCode(t, http.StatusUnsupportedMediaType, response.StatusCode)
	}
}

func TestEmptyBody(t *testing.T) {
	for _, handler := range handlerFuncs {
		t.Logf("testing func %s, empty body", getFuncName(handler))
		response = testRequest(http.MethodPost, handler, "", defaultMediaType)
		_ = invalidStatusCode(t, http.StatusBadRequest, response.StatusCode)
	}
}

func TestInvalidRequestBody(t *testing.T) {
	for _, handler := range handlerFuncs {
		t.Logf("testing func %s, invalid request", getFuncName(handler))
		invalidBody, _ := json.Marshal(invalidResource)
		response = testRequest(http.MethodPost, handler, string(invalidBody), defaultMediaType)
		_ = invalidStatusCode(t, http.StatusBadRequest, response.StatusCode)
	}
}

// Shared handler tests

func TestInvalidRequestResource(t *testing.T) {
	for _, handler := range handlerFuncs {
		t.Logf("testing func %s, invalid request resource", getFuncName(handler))
		rawObject, _ = json.Marshal(invalidResource)
		admissionReview, _ = admissionReviewFactory(invalidResourceGVR, rawObject)
		response = testRequest(http.MethodPost, handler, string(admissionReview[:]), defaultMediaType)
		_ = invalidStatusCode(t, http.StatusInternalServerError, response.StatusCode)
	}
}

func TestInvalidRequestObject(t *testing.T) {
	for _, handler := range handlerFuncs {
		t.Logf("testing func %s, valid request, invalid kind", getFuncName(handler))
		rawObject, _ = json.Marshal(invalidResource)
		admissionReview, _ = admissionReviewFactory(resourceGVR, rawObject)
		response = testRequest(http.MethodPost, handler, string(admissionReview[:]), defaultMediaType)
		_ = invalidStatusCode(t, http.StatusInternalServerError, response.StatusCode)
	}
}

func TestMissingPodAnnotation(t *testing.T) {
	for _, handler := range handlerFuncs {
		t.Logf("testing func %s, missing annotation", getFuncName(handler))
		rawObject, _ = podFactory(namespace, nil, nil, 0)
		admissionReview, _ = admissionReviewFactory(resourceGVR, rawObject)
		response = testRequest(http.MethodPost, handler, string(admissionReview[:]), defaultMediaType)
		_ = invalidStatusCode(t, http.StatusOK, response.StatusCode)
	}
}

// Mutation handler tests

func TestInvalidNamespace(t *testing.T) {
	t.Logf("testing func %s, invalid request object", getFuncName(handleMutate))
	rawObject, _ = podFactory(invalidNamespace, nil, annotations, 0)
	admissionReview, _ = admissionReviewFactory(resourceGVR, rawObject)
	response = testRequest(http.MethodPost, handleMutate, string(admissionReview[:]), defaultMediaType)
	_ = invalidStatusCode(t, http.StatusInternalServerError, response.StatusCode)
}

func TestInvalidCABundleURL(t *testing.T) {
	t.Logf("testing func %s, invalid ca bundle url", getFuncName(handleMutate))
	_ = os.Setenv(keyCABundleURL, "https://invalid.local")
	defer func() { _ = os.Setenv(keyCABundleURL, caBundleUrl) }()
	rawObject, _ = podFactory(namespace, nil, annotations, 2)
	admissionReview, _ = admissionReviewFactory(resourceGVR, rawObject)
	response = testRequest(http.MethodPost, handleMutate, string(admissionReview[:]), defaultMediaType)
	_ = invalidStatusCode(t, http.StatusInternalServerError, response.StatusCode)
}

func TestInvalidCABundle(t *testing.T) {
	t.Logf("testing func %s, invalid ca bundle", getFuncName(handleMutate))
	_ = os.Setenv(keyCABundleURL, "https://example.com")
	defer func() { _ = os.Setenv(keyCABundleURL, caBundleUrl) }()
	rawObject, _ = podFactory(namespace, nil, annotations, 2)
	admissionReview, _ = admissionReviewFactory(resourceGVR, rawObject)
	response = testRequest(http.MethodPost, handleMutate, string(admissionReview[:]), defaultMediaType)
	_ = invalidStatusCode(t, http.StatusInternalServerError, response.StatusCode)
}

func TestValidRequestWithoutNamespace(t *testing.T) {
	t.Logf("testing func %s, valid request without namespace", getFuncName(handleMutate))
	rawObject, _ = podFactory("", nil, annotations, 2)
	admissionReview, _ = admissionReviewFactory(resourceGVR, rawObject)
	response = testRequest(http.MethodPost, handleMutate, string(admissionReview[:]), defaultMediaType)
	deleteConfigMap(namespace)
	if invalidStatusCode(t, http.StatusOK, response.StatusCode) {
		return
	}
	_ = invalidResponseBody(t, response.Body)
}

func TestValidRequest(t *testing.T) {
	t.Logf("testing func %s, valid request", getFuncName(handleMutate))
	rawObject, _ = podFactory(namespace, nil, annotations, 2)
	admissionReview, _ = admissionReviewFactory(resourceGVR, rawObject)
	response = testRequest(http.MethodPost, handleMutate, string(admissionReview[:]), defaultMediaType)
	deleteConfigMap(namespace)
	if invalidStatusCode(t, http.StatusOK, response.StatusCode) {
		return
	}
	_ = invalidResponseBody(t, response.Body)
}
