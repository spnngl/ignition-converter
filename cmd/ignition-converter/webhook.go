package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// A Secret is only touched when its "format" key holds this value, which
	// is how Cluster API's kubeadm bootstrap provider flags Ignition user data.
	formatKey     = "format"
	formatValue   = "ignition"
	userDataKey   = "value"
	userDataPath  = "/data/" + userDataKey
	maxReviewSize = 8 << 20 // a Secret is capped at 1MiB; leave room for base64 and the envelope
)

// jsonPatchOp is one RFC 6902 operation. []byte marshals as the base64 string
// a Secret's data field expects.
type jsonPatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value []byte `json:"value"`
}

// handleMutate answers an admission.k8s.io/v1 AdmissionReview for Secrets.
func handleMutate(w http.ResponseWriter, r *http.Request) {
	var review admissionv1.AdmissionReview
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReviewSize)).Decode(&review); err != nil {
		http.Error(w, fmt.Sprintf("decoding AdmissionReview: %v", err), http.StatusBadRequest)
		return
	}
	if review.Request == nil {
		http.Error(w, "AdmissionReview has no request", http.StatusBadRequest)
		return
	}

	resp := mutate(review.Request)
	resp.UID = review.Request.UID
	review.Response = &resp
	review.Request = nil // not needed by the API server, and it echoes the Secret

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(review); err != nil {
		log.Printf("writing AdmissionReview: %v", err)
	}
}

// mutate allows every request, patching Ignition 2.x user data to spec 3.0 and
// denying data it cannot convert (the webhook fails closed, so a broken
// bootstrap Secret never reaches a machine that would reject it at boot).
func mutate(req *admissionv1.AdmissionRequest) admissionv1.AdmissionResponse {
	if req.Operation != admissionv1.Create && req.Operation != admissionv1.Update {
		return admissionv1.AdmissionResponse{Allowed: true}
	}

	var secret corev1.Secret
	if err := json.Unmarshal(req.Object.Raw, &secret); err != nil {
		return deny(fmt.Errorf("decoding Secret: %w", err))
	}
	if string(secret.Data[formatKey]) != formatValue {
		return admissionv1.AdmissionResponse{Allowed: true}
	}

	out, converted, err := convert(secret.Data[userDataKey])
	if err != nil {
		return deny(fmt.Errorf("secret %s/%s: %w", req.Namespace, req.Name, err))
	}
	if !converted {
		return admissionv1.AdmissionResponse{Allowed: true}
	}

	patch, err := json.Marshal([]jsonPatchOp{{Op: "replace", Path: userDataPath, Value: out}})
	if err != nil {
		return deny(fmt.Errorf("marshaling patch: %w", err))
	}
	patchType := admissionv1.PatchTypeJSONPatch
	return admissionv1.AdmissionResponse{Allowed: true, Patch: patch, PatchType: &patchType}
}

func deny(err error) admissionv1.AdmissionResponse {
	log.Printf("denied: %v", err)
	return admissionv1.AdmissionResponse{
		Allowed: false,
		Result:  &metav1.Status{Code: http.StatusBadRequest, Message: err.Error()},
	}
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	_, _ = io.WriteString(w, "ok")
}
