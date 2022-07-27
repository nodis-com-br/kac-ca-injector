package kac

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

var (
	configMap = &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
	}
	configMapsGVR = metav1.GroupVersionResource{
		Version:  "v1",
		Resource: "ConfigMaps",
	}
	pod = corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod"},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{},
			Containers: []corev1.Container{
				{
					VolumeMounts: []corev1.VolumeMount{},
				},
			},
		},
	}
	caBundleURL string
)

func init() {
	_ = os.Setenv(keyConfigMapName, "ca-bundle")
	_ = os.Setenv(keyCABundleFilename, "ca_bundle.pem")
	_ = os.Setenv(keyCABundleAnnotation, "example.com/ca-injector")
	_ = os.Setenv(keyCABundleURL, "https://curl.se/ca/cacert.pem")
	_ = os.Setenv(keyPodNamespace, "example")
	caBundleURL = os.Getenv(keyCABundleURL)
}

func admissionReviewFactory(gvr metav1.GroupVersionResource, rawObject []byte) ([]byte, error) {
	return json.Marshal(admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Request: &admissionv1.AdmissionRequest{
			Resource: gvr,
			Object: runtime.RawExtension{
				Raw: rawObject,
			},
		},
	})
}

func fakeRequest(ctx context.Context, r *gin.Engine, method string, route string, rawBody string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(method, route, strings.NewReader(rawBody))
	req = req.WithContext(ctx)
	r.ServeHTTP(w, req)
	return w
}

func Test_HealthcheckRoute(t *testing.T) {
	router := NewRouter()
	w := fakeRequest(context.Background(), router, http.MethodGet, "/health", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, `{"status":"ok"}`, w.Body.String())
}

func Test_ReviewerRoutes(t *testing.T) {

	ctx := context.Background()
	router := NewRouter()

	encodedConfigMap, _ := json.Marshal(configMap)
	encodedPodNoAnnotationNoNamespace, _ := json.Marshal(pod)

	pod.Annotations = map[string]string{os.Getenv(keyCABundleAnnotation): "true"}
	encodedPodNoNamespace, _ := json.Marshal(pod)

	pod.Namespace = os.Getenv(keyPodNamespace)
	encodedPod, _ := json.Marshal(pod)

	arInvalidResource, _ := admissionReviewFactory(configMapsGVR, encodedConfigMap)
	arInvalidResourceKind, _ := admissionReviewFactory(podsGVR, encodedConfigMap)
	arValidRequestNoAnnotationNoNamespace, _ := admissionReviewFactory(podsGVR, encodedPodNoAnnotationNoNamespace)

	arValidRequestNoNamespace, _ := admissionReviewFactory(podsGVR, encodedPodNoNamespace)
	arValidRequest, _ := admissionReviewFactory(podsGVR, encodedPod)

	for _, route := range []string{"/mutate", "/validate"} {
		t.Run("test route "+route+" with nil body", func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodPost, route, nil)
			router.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
		t.Run("test route "+route+" with empty body", func(t *testing.T) {
			w := fakeRequest(ctx, router, http.MethodPost, route, "")
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
		t.Run("test route "+route+" with invalid body", func(t *testing.T) {
			w := fakeRequest(ctx, router, http.MethodPost, route, string(encodedConfigMap))
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}

	t.Run("test route /validate with valid request", func(t *testing.T) {
		w := fakeRequest(ctx, router, http.MethodPost, "/validate", string(arValidRequest))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("test route /mutate with invalid admission request resource", func(t *testing.T) {
		w := fakeRequest(ctx, router, http.MethodPost, "/mutate", string(arInvalidResource))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("test route /mutate with invalid admission request resource kind", func(t *testing.T) {
		w := fakeRequest(ctx, router, http.MethodPost, "/mutate", string(arInvalidResourceKind))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("test route /mutate with valid request, no fake client", func(t *testing.T) {
		w := fakeRequest(ctx, router, http.MethodPost, "/mutate", string(arValidRequest))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("test route /mutate with invalid bundle url", func(t *testing.T) {
		_ = os.Setenv(keyCABundleURL, "https://invalid.local")
		ctx = context.WithValue(ctx, keyFake, true)
		defer func() {
			_ = os.Setenv(keyCABundleURL, caBundleURL)
			ctx = context.WithValue(ctx, keyFake, false)
		}()
		w := fakeRequest(ctx, router, http.MethodPost, "/mutate", string(arValidRequest))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("test route /mutate with valid request, no fake client", func(t *testing.T) {
		_ = os.Setenv(keyCABundleURL, "https://example.com")
		ctx = context.WithValue(ctx, keyFake, true)
		defer func() {
			_ = os.Setenv(keyCABundleURL, caBundleURL)
			ctx = context.WithValue(ctx, keyFake, false)
		}()
		w := fakeRequest(ctx, router, http.MethodPost, "/mutate", string(arValidRequest))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("test route /mutate with valid request missing annotation", func(t *testing.T) {
		w := fakeRequest(ctx, router, http.MethodPost, "/mutate", string(arValidRequestNoAnnotationNoNamespace))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("test route /mutate with valid request missing namespace", func(t *testing.T) {
		ctx = context.WithValue(ctx, keyFake, true)
		w := fakeRequest(ctx, router, http.MethodPost, "/mutate", string(arValidRequestNoNamespace))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("test route /mutate with valid request", func(t *testing.T) {
		ctx = context.WithValue(ctx, keyFake, true)
		w := fakeRequest(ctx, router, http.MethodPost, "/mutate", string(arValidRequest))
		assert.Equal(t, http.StatusOK, w.Code)
	})

}
