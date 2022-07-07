package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/wI2L/jsondiff"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sRuntime "k8s.io/apimachinery/pkg/runtime"
	k8sSerializer "k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	keyCABundleURL        = "CA_BUNDLE_URL"
	keyConfigMapName      = "CA_BUNDLE_CONFIGMAP"
	keyCABundleFilename   = "CA_BUNDLE_FILENAME"
	keyCABundleAnnotation = "CA_BUNDLE_ANNOTATION"
)

var (
	runtimeScheme = k8sRuntime.NewScheme()
	codecFactory  = k8sSerializer.NewCodecFactory(runtimeScheme)
	deserializer  = codecFactory.UniversalDeserializer()
	resourceGVR   = metav1.GroupVersionResource{Version: "v1", Resource: "pods"}
)

type AdmitFunc func(admissionv1.AdmissionReview) *admissionv1.AdmissionResponse

type HandleFunc func(w http.ResponseWriter, r *http.Request)

// add kind AdmissionReview in scheme
func init() {
	_ = corev1.AddToScheme(runtimeScheme)
	_ = admissionv1.AddToScheme(runtimeScheme)
}

func setLogLevel() {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if os.Getenv("DEBUG") == "true" {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}
}

func getKubernetesClientSet() (*kubernetes.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		config, _ = clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	}
	return kubernetes.NewForConfig(config)
}

func validateAndDeserialize(ar admissionv1.AdmissionReview) *corev1.Pod {
	// Validate resource type
	if ar.Request.Resource != resourceGVR {
		msg := fmt.Sprintf("expect resource to be %s", resourceGVR)
		log.Error().Msg(msg)
		return nil
	}
	// Deserialize pod from AdmissionRequest object
	pod := corev1.Pod{}
	_, _, _ = deserializer.Decode(ar.Request.Object.Raw, nil, &pod)
	if pod.Name == "" {
		msg := fmt.Sprintf("deserialized pod is empty: %v", pod)
		log.Error().Msg(msg)
		return nil
	} else {
		return &pod
	}
}

func serve(w http.ResponseWriter, r *http.Request, admitFunc AdmitFunc) {

	setLogLevel()

	// verify the request method
	if r.Method != http.MethodPost {
		msg := fmt.Sprintf("%s method not allowed", r.Method)
		log.Error().Msg(msg)
		http.Error(w, msg, http.StatusMethodNotAllowed)
		return
	}

	// verify the content type
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		msg := fmt.Sprintf("Content-Type=%s, expect application/json", contentType)
		log.Error().Msg(msg)
		http.Error(w, msg, http.StatusUnsupportedMediaType)
		return
	}

	var body []byte
	if r.Body != nil {
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}
	}

	log.Debug().Msgf("handling request: %s", body)
	var responseObj k8sRuntime.Object
	if obj, gvk, err := deserializer.Decode(body, nil, nil); err != nil {
		msg := fmt.Sprintf("Request could not be decoded: %v", err)
		log.Error().Msg(msg)
		http.Error(w, msg, http.StatusBadRequest)
		return
	} else {
		requestedAdmissionReview, ok := obj.(*admissionv1.AdmissionReview)
		if !ok {
			msg := fmt.Sprintf("Expected v1.AdmissionReview but got: %T", obj)
			log.Error().Msg(msg)
			http.Error(w, msg, http.StatusBadRequest)
			return
		}
		responseAdmissionReview := &admissionv1.AdmissionReview{}
		responseAdmissionReview.SetGroupVersionKind(*gvk)
		responseAdmissionReview.Response = admitFunc(*requestedAdmissionReview)
		if responseAdmissionReview.Response == nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		responseAdmissionReview.Response.UID = requestedAdmissionReview.Request.UID
		responseObj = responseAdmissionReview
	}
	log.Debug().Msgf("sending response: %v", responseObj)
	respBytes, _ := json.Marshal(responseObj)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(respBytes)
}

func handleMutate(w http.ResponseWriter, r *http.Request) {
	serve(w, r, mutate)
}

func handleValidate(w http.ResponseWriter, r *http.Request) {
	serve(w, r, validate)
}

func mutate(ar admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {

	configMapName := os.Getenv(keyConfigMapName)
	caBundleFilename := os.Getenv(keyCABundleFilename)

	// Deserialize and copy request object
	pod := validateAndDeserialize(ar)
	if pod == nil {
		return nil
	}
	newPod := pod.DeepCopy()

	// Inject ca bundle configmap if pods contains annotation
	if pod.Annotations[os.Getenv(keyCABundleAnnotation)] == "true" {

		log.Info().Msgf("mutating pod %s on namespace %s", pod.Name, pod.Namespace)

		// Connect to to kubernetes cluster to check if configmap exists
		clientSet, _ := getKubernetesClientSet()
		ctx := context.Background()
		configMap, _ := clientSet.CoreV1().ConfigMaps(fmt.Sprint(pod.Namespace)).Get(ctx, fmt.Sprint(configMapName), metav1.GetOptions{})

		// Create configmap if not found
		if configMap.Name == "" {
			log.Info().Msgf("creating configmap %s on namespace %s", configMapName, pod.Namespace)
			resp, err := http.Get(os.Getenv(keyCABundleURL))
			if err != nil {
				log.Error().Msgf("error fetching ca bundle: %v", err)
				return nil
			}
			body, _ := ioutil.ReadAll(resp.Body)
			if !strings.Contains(string(body), "-----BEGIN CERTIFICATE-----") {
				log.Error().Msgf("invalid ca bundle: %v", string(body))
				return nil
			}
			defer func() { _ = resp.Body.Close() }()
			if configMap, err = clientSet.CoreV1().ConfigMaps(pod.Namespace).Create(ctx, &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprint(configMapName),
					Namespace: pod.Namespace,
				},
				Data: map[string]string{
					caBundleFilename: string(body),
				},
			}, metav1.CreateOptions{}); err != nil {
				log.Error().Msgf("error creating configmap: %v", err)
				return nil
			}
		}

		// Add Volume to new pod
		newPod.Spec.Volumes = append(newPod.Spec.Volumes, corev1.Volume{
			Name: configMap.Name,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: configMap.Name,
					},
				},
			},
		})

		// Add VolumeMounts to new pod containers
		for i := range newPod.Spec.Containers {
			newPod.Spec.Containers[i].VolumeMounts = append(newPod.Spec.Containers[i].VolumeMounts, corev1.VolumeMount{
				Name:      configMap.Name,
				MountPath: "/etc/ssl/certs/" + caBundleFilename,
				SubPath:   caBundleFilename,
			})
		}

	}

	// Create mutation patch
	patch, _ := jsondiff.Compare(pod, newPod)
	encodedPatch, _ := json.Marshal(patch)

	// Return AdmissionReview object with AdmissionResponse
	pt := admissionv1.PatchTypeJSONPatch
	return &admissionv1.AdmissionResponse{Allowed: true, PatchType: &pt, Patch: encodedPatch}
}

func validate(ar admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	pod := validateAndDeserialize(ar)
	if pod == nil {
		return nil
	}
	return &admissionv1.AdmissionResponse{Allowed: true}
}

func main() {
	var tlsKey, tlsCert string
	flag.StringVar(&tlsKey, "tlsKey", "/certs/tls.key", "Path to the TLS key")
	flag.StringVar(&tlsCert, "tlsCert", "/certs/tls.crt", "Path to the TLS certificate")
	flag.Parse()
	http.HandleFunc("/mutate", handleMutate)
	http.HandleFunc("/validate", handleValidate)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { return })
	log.Info().Msg("Server started ...")
	log.Fatal().Err(http.ListenAndServeTLS(":8443", tlsCert, tlsKey, nil)).Msg("webhook server exited")
}
