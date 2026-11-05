package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func review(t *testing.T, op admissionv1.Operation, data map[string][]byte) admissionv1.AdmissionResponse {
	t.Helper()
	secret, err := json.Marshal(corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: "worker-0", Namespace: "default"},
		Type:       "cluster.x-k8s.io/secret",
		Data:       data,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{APIVersion: "admission.k8s.io/v1", Kind: "AdmissionReview"},
		Request:  &admissionv1.AdmissionRequest{UID: "abc", Operation: op, Name: "worker-0", Namespace: "default", Object: runtime.RawExtension{Raw: secret}},
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	handleMutate(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mutate", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var out admissionv1.AdmissionReview
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Response == nil || out.Response.UID != "abc" || out.APIVersion != "admission.k8s.io/v1" {
		t.Fatalf("bad envelope: %+v", out)
	}
	return *out.Response
}

func TestMutate(t *testing.T) {
	v2 := readFixture(t, "testdata/kubeadm-join.json")
	v3 := []byte(`{"ignition":{"version":"3.4.0"}}`)

	t.Run("ignition 2.3 is patched", func(t *testing.T) {
		for _, op := range []admissionv1.Operation{admissionv1.Create, admissionv1.Update} {
			resp := review(t, op, map[string][]byte{"format": []byte("ignition"), "value": v2})
			if !resp.Allowed || resp.PatchType == nil || *resp.PatchType != admissionv1.PatchTypeJSONPatch {
				t.Fatalf("%s: want allowed JSONPatch, got %+v", op, resp)
			}
			var ops []jsonPatchOp
			if err := json.Unmarshal(resp.Patch, &ops); err != nil {
				t.Fatal(err)
			}
			if len(ops) != 1 || ops[0].Op != "replace" || ops[0].Path != "/data/value" {
				t.Fatalf("unexpected patch: %s", resp.Patch)
			}
			parseV3(t, ops[0].Value)

			// Once patched, the object is left alone.
			again := review(t, op, map[string][]byte{"format": []byte("ignition"), "value": ops[0].Value})
			if !again.Allowed || again.Patch != nil {
				t.Errorf("patched value must pass through, got %+v", again)
			}
		}
	})

	for name, tc := range map[string]struct {
		op   admissionv1.Operation
		data map[string][]byte
	}{
		"cloud-config is untouched": {admissionv1.Create, map[string][]byte{"format": []byte("cloud-config"), "value": []byte("#cloud-config")}},
		"no format is untouched":    {admissionv1.Create, map[string][]byte{"value": []byte("x")}},
		"ignition 3.x is untouched": {admissionv1.Update, map[string][]byte{"format": []byte("ignition"), "value": v3}},
		"delete is untouched":       {admissionv1.Delete, map[string][]byte{"format": []byte("ignition"), "value": []byte("x")}},
	} {
		t.Run(name, func(t *testing.T) {
			if resp := review(t, tc.op, tc.data); !resp.Allowed || resp.Patch != nil {
				t.Errorf("want allowed without patch, got %+v", resp)
			}
		})
	}

	for name, data := range map[string]map[string][]byte{
		"garbage is denied":        {"format": []byte("ignition"), "value": []byte("garbage")},
		"missing value is denied":  {"format": []byte("ignition")},
		"invalid config is denied": {"format": []byte("ignition"), "value": []byte(`{"ignition":{"version":"2.3.0"},"storage":{"files":[{"path":"relative"}]}}`)},
	} {
		t.Run(name, func(t *testing.T) {
			resp := review(t, admissionv1.Create, data)
			if resp.Allowed || resp.Result == nil || resp.Result.Message == "" {
				t.Errorf("want denial with a message, got %+v", resp)
			}
		})
	}
}

func TestHandleMutateBadRequest(t *testing.T) {
	for name, body := range map[string]string{"not json": "x", "no request": "{}"} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handleMutate(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mutate", bytes.NewReader([]byte(body))))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}
