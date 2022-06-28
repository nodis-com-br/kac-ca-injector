package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	paths            = []string{"/validate", "/mutate"}
	forbiddenMethods = []string{"GET", "PUT", "DELETE"}
)

func testAdmission(allow bool, msg string) admissionv1.AdmissionReview {
	return admissionv1.AdmissionReview{
		Response: &admissionv1.AdmissionResponse{
			Allowed: allow,
			Result: &metav1.Status{
				Message: msg,
			},
		},
	}
}

func podFactory(labels map[string]string, annotations map[string]string, volumeCount int) corev1.Pod {

	podDefinition := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
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

	return podDefinition

}

func jsonMarshal(allow bool, msg string) []byte {
	// Marshal JSON into objects
	validate, err := json.Marshal(testAdmission(allow, msg))
	if err != nil {
		fmt.Printf("Failed to encode response: %v", err)
	}
	return validate
}

func TestForbiddenMethods(t *testing.T) {
	for _, method := range forbiddenMethods {
		for _, path := range paths {
			func() {
				req := httptest.NewRequest(method, path, nil)
				w := httptest.NewRecorder()
				serveValidate(w, req)
				res := w.Result()
				defer func() { _ = res.Body.Close() }()
				if res.Status != "405 Method Not Allowed" {
					t.Errorf("expected error 405 got %s", res.Status)
				}
			}()
		}
	}
}
