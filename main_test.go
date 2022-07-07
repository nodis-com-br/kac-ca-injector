package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	handlerFuncs    = []HandleFunc{handleValidate, handleMutate}
	target          = "/"
	request         *http.Request
	response        *http.Response
	bodyReader      *strings.Reader
	rawObject       []byte
	admissionReview []byte
	invalidResource = corev1.ConfigMap{
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
	_ = os.Setenv("DEBUG", "true")
	_ = os.Setenv("KUBECONFIG", "./kubeconfig")
	_ = os.Setenv(keyConfigMapName, "ca-bundle")
	_ = os.Setenv(keyCABundleFilename, "ca_bundle.pem")
	_ = os.Setenv(keyCABundleAnnotation, "example.com/ca-injector")
	_ = os.Setenv(keyCABundleURL, "https://curl.se/ca/cacert.pem")
	_ = os.Setenv(keyPodNamespace, "botland")
}

// Helper functions

func getFuncName(temp interface{}) string {
	strs := strings.Split(runtime.FuncForPC(reflect.ValueOf(temp).Pointer()).Name(), ".")
	return strs[len(strs)-1]
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

func requestProcessor(req *http.Request, handlerFunc HandleFunc) *http.Response {
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
			request = httptest.NewRequest(method, target, nil)
			response = requestProcessor(request, handler)
			if response.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("expected status %d got %d", http.StatusMethodNotAllowed, response.StatusCode)
			}
		}
	}
}

func TestMissingMediaType(t *testing.T) {
	for _, handler := range handlerFuncs {
		t.Logf("testing func %s, missing media type", getFuncName(handler))
		request = httptest.NewRequest(http.MethodPost, target, nil)
		response = requestProcessor(request, handler)
		if response.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("expected status %d got %d", http.StatusUnsupportedMediaType, response.StatusCode)
		}
	}
}

func TestEmptyBody(t *testing.T) {
	for _, handler := range handlerFuncs {
		t.Logf("testing func %s, empty body", getFuncName(handler))
		request = httptest.NewRequest(http.MethodPost, target, nil)
		request.Header.Set("Content-Type", "application/json")
		response = requestProcessor(request, handler)
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("expected status %d got %d", http.StatusBadRequest, response.StatusCode)
		}
	}
}

func TestInvalidRequestBody(t *testing.T) {
	for _, handler := range handlerFuncs {
		t.Logf("testing func %s, invalid request", getFuncName(handler))
		invalidBody, _ := json.Marshal(invalidResource)
		bodyReader = strings.NewReader(string(invalidBody))
		request = httptest.NewRequest(http.MethodPost, target, bodyReader)
		request.Header.Set("Content-Type", "application/json")
		response = requestProcessor(request, handler)
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("expected status %d got %d", http.StatusBadRequest, response.StatusCode)
		}
	}
}

// Shared handler tests

func TestInvalidRequestResource(t *testing.T) {
	for _, handler := range handlerFuncs {
		t.Logf("testing func %s, invalid request resource", getFuncName(handler))
		rawObject, _ = json.Marshal(invalidResource)
		admissionReview, _ = admissionReviewFactory(invalidResourceGVR, rawObject)
		bodyReader = strings.NewReader(string(admissionReview[:]))
		request = httptest.NewRequest(http.MethodPost, target, bodyReader)
		request.Header.Set("Content-Type", "application/json")
		response = requestProcessor(request, handler)
		if response.StatusCode != http.StatusInternalServerError {
			t.Errorf("expected error %d got %d", http.StatusInternalServerError, response.StatusCode)
			return
		}
	}
}

func TestInvalidRequestObject(t *testing.T) {
	for _, handler := range handlerFuncs {
		t.Logf("testing func %s, valid request, invalid kind", getFuncName(handler))
		rawObject, _ = json.Marshal(invalidResource)
		admissionReview, _ = admissionReviewFactory(resourceGVR, rawObject)
		bodyReader = strings.NewReader(string(admissionReview[:]))
		request = httptest.NewRequest(http.MethodPost, target, bodyReader)
		request.Header.Set("Content-Type", "application/json")
		response = requestProcessor(request, handler)
		if response.StatusCode != http.StatusInternalServerError {
			t.Errorf("expected status %d got %d", http.StatusInternalServerError, response.StatusCode)
			return
		}
	}
}

func TestMissingPodAnnotation(t *testing.T) {
	for _, handler := range handlerFuncs {
		t.Logf("testing func %s, missing annotation", getFuncName(handler))
		rawObject, _ = podFactory("default", nil, nil, 0)
		admissionReview, _ = admissionReviewFactory(resourceGVR, rawObject)
		bodyReader = strings.NewReader(string(admissionReview[:]))
		request = httptest.NewRequest(http.MethodPost, target, bodyReader)
		request.Header.Set("Content-Type", "application/json")
		response = requestProcessor(request, handler)
		if response.StatusCode != http.StatusOK {
			t.Errorf("expected status %d got %d", http.StatusOK, response.StatusCode)
		}
	}
}

// Mutation handler tests

func TestInvalidNamespace(t *testing.T) {
	t.Logf("testing func %s, invalid request object", getFuncName(handleMutate))
	annotations := map[string]string{os.Getenv(keyCABundleAnnotation): "true"}
	rawObject, _ = podFactory("invalid", nil, annotations, 0)
	admissionReview, _ = admissionReviewFactory(resourceGVR, rawObject)
	bodyReader = strings.NewReader(string(admissionReview[:]))
	request = httptest.NewRequest(http.MethodPost, target, bodyReader)
	request.Header.Set("Content-Type", "application/json")
	response = requestProcessor(request, handleMutate)
	if response.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected error %d got %d", http.StatusInternalServerError, response.StatusCode)
		return
	}
}

func TestInvalidCABundleURL(t *testing.T) {
	t.Logf("testing func %s, invalid ca bundle url", getFuncName(handleMutate))
	caBundleUrl := os.Getenv(keyCABundleURL)
	annotations := map[string]string{os.Getenv(keyCABundleAnnotation): "true"}
	_ = os.Setenv(keyCABundleURL, "https://invalid.local")
	rawObject, _ = podFactory("default", nil, annotations, 2)
	admissionReview, _ = admissionReviewFactory(resourceGVR, rawObject)
	bodyReader = strings.NewReader(string(admissionReview[:]))
	request = httptest.NewRequest(http.MethodPost, target, bodyReader)
	request.Header.Set("Content-Type", "application/json")
	response = requestProcessor(request, handleMutate)
	if response.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected error %d got %d", http.StatusInternalServerError, response.StatusCode)
		return
	}
	_ = os.Setenv(keyCABundleURL, caBundleUrl)

}

func TestInvalidCABundle(t *testing.T) {
	t.Logf("testing func %s, invalid ca bundle", getFuncName(handleMutate))
	caBundleUrl := os.Getenv(keyCABundleURL)
	annotations := map[string]string{os.Getenv(keyCABundleAnnotation): "true"}
	_ = os.Setenv(keyCABundleURL, "https://example.com")
	rawObject, _ = podFactory("default", nil, annotations, 2)
	admissionReview, _ = admissionReviewFactory(resourceGVR, rawObject)
	bodyReader = strings.NewReader(string(admissionReview[:]))
	request = httptest.NewRequest(http.MethodPost, target, bodyReader)
	request.Header.Set("Content-Type", "application/json")
	response = requestProcessor(request, handleMutate)
	if response.StatusCode != http.StatusInternalServerError {
		t.Errorf("expected status %d got %d", http.StatusInternalServerError, response.StatusCode)
		return
	}
	_ = os.Setenv(keyCABundleURL, caBundleUrl)

}

func TestValidRequestWithoutNamespace(t *testing.T) {
	t.Logf("testing func %s, valid request without namespace", getFuncName(handleMutate))
	annotations := map[string]string{os.Getenv(keyCABundleAnnotation): "true"}
	rawObject, _ = podFactory("", nil, annotations, 2)
	admissionReview, _ = admissionReviewFactory(resourceGVR, rawObject)
	bodyReader = strings.NewReader(string(admissionReview[:]))
	request = httptest.NewRequest(http.MethodPost, target, bodyReader)
	request.Header.Set("Content-Type", "application/json")
	response = requestProcessor(request, handleMutate)
	clientSet, _ := getKubernetesClientSet()
	_ = clientSet.CoreV1().ConfigMaps("default").Delete(context.Background(), os.Getenv(keyConfigMapName), metav1.DeleteOptions{})
	if response.StatusCode != http.StatusOK {
		t.Errorf("expected status %d got %d", http.StatusOK, response.StatusCode)
		return
	}
	body, _ := ioutil.ReadAll(response.Body)
	admissionReviewResponse := admissionv1.AdmissionReview{}
	_ = json.Unmarshal(body, &admissionReviewResponse)
	var operations []jsonpatch.Patch
	_ = json.Unmarshal(admissionReviewResponse.Response.Patch, &operations)
	if len(operations) == 0 {
		t.Errorf("patch operations array is empty")
	}
}

func TestValidRequest(t *testing.T) {
	t.Logf("testing func %s, valid request", getFuncName(handleMutate))
	annotations := map[string]string{os.Getenv(keyCABundleAnnotation): "true"}
	rawObject, _ = podFactory("default", nil, annotations, 2)
	admissionReview, _ = admissionReviewFactory(resourceGVR, rawObject)
	bodyReader = strings.NewReader(string(admissionReview[:]))
	request = httptest.NewRequest(http.MethodPost, target, bodyReader)
	request.Header.Set("Content-Type", "application/json")
	response = requestProcessor(request, handleMutate)
	clientSet, _ := getKubernetesClientSet()
	_ = clientSet.CoreV1().ConfigMaps("default").Delete(context.Background(), os.Getenv(keyConfigMapName), metav1.DeleteOptions{})
	if response.StatusCode != http.StatusOK {
		t.Errorf("expected status %d got %d", http.StatusOK, response.StatusCode)
		return
	}
	body, _ := ioutil.ReadAll(response.Body)
	admissionReviewResponse := admissionv1.AdmissionReview{}
	_ = json.Unmarshal(body, &admissionReviewResponse)
	var operations []jsonpatch.Patch
	_ = json.Unmarshal(admissionReviewResponse.Response.Patch, &operations)
	if len(operations) == 0 {
		t.Errorf("patch operations array is empty")
	}
}
