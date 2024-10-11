package admissionwebhook

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

type AdmissionHandler struct {
	scheme *runtime.Scheme
	codecs serializer.CodecFactory
}

func NewAdmissionHandler() *AdmissionHandler {
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)
	codecs := serializer.NewCodecFactory(scheme)
	return &AdmissionHandler{
		scheme: scheme,
		codecs: codecs,
	}
}

func (h *AdmissionHandler) MutationHandler(w http.ResponseWriter, r *http.Request) {
	var admissionReviewReq admissionv1.AdmissionReview

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("could not read request body: %v", err), http.StatusBadRequest)
		return
	}

	decoder := h.codecs.UniversalDeserializer()
	if _, _, err := decoder.Decode(body, nil, &admissionReviewReq); err != nil {
		http.Error(w, fmt.Sprintf("could not deserialize request: %v", err), http.StatusBadRequest)
		return
	}

	raw := admissionReviewReq.Request.Object.Raw
	pod := corev1.Pod{}
	if err := json.Unmarshal(raw, &pod); err != nil {
		http.Error(w, fmt.Sprintf("could not unmarshal pod: %v", err), http.StatusBadRequest)
		return
	}

	admissionReviewResp := admissionv1.AdmissionReview{
		Response: &admissionv1.AdmissionResponse{
			UID: admissionReviewReq.Request.UID,
		},
	}

	if value, ok := pod.Annotations["consul-register"]; ok && value == "true" {
		addConsulRegisterInitContainer(&pod)

		marshaledPod, err := json.Marshal(pod)
		if err != nil {
			http.Error(w, fmt.Sprintf("could not marshal pod: %v", err), http.StatusInternalServerError)
			return
		}

		admissionReviewResp.Response.Allowed = true
		admissionReviewResp.Response.Patch = marshaledPod
		pt := admissionv1.PatchTypeJSONPatch
		admissionReviewResp.Response.PatchType = &pt
	} else {
		admissionReviewResp.Response.Allowed = true
	}

	respBytes, err := json.Marshal(admissionReviewResp)
	if err != nil {
		http.Error(w, fmt.Sprintf("could not marshal response: %v", err), http.StatusInternalServerError)
		return
	}

	w.Write(respBytes)

}

func addConsulRegisterInitContainer(pod *corev1.Pod) {
	initContainer := corev1.Container{
		Name:  "consul-register",
		Image: "curlimages/curl:7.77.0",
		Command: []string{
			"sh",
			"-c",
			`curl --request PUT --data '{
				"ID": "test-flask-service-'$POD_IP'",
				"Name": "test-flask-service",
				"Address": "'$POD_IP'",
				"Meta": {
					"node_ip": "'$NODE_IP'",
					"pod_ip": "'$POD_IP'"
				},
				"Port": 8080,
				"Check": {
					"http": "http://'$POD_IP':8080",
					"interval": "10s"
				}
			}' http://consul-consul-server.consul:8500/v1/agent/service/register`,
		},
		Env: []corev1.EnvVar{
			{
				Name: "POD_IP",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "status.podIP",
					},
				},
			},
			{
				Name: "NODE_IP",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "status.hostIP",
					},
				},
			},
		},
	}

	pod.Spec.InitContainers = append(pod.Spec.InitContainers, initContainer)
}

func main() {
	admissionHandler := NewAdmissionHandler()

	http.HandleFunc("/mutate", admissionHandler.MutationHandler)

	fmt.Println("Starting webhook server on port 8080...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		fmt.Printf("Error starting server: %v\n", err)
	}
}
