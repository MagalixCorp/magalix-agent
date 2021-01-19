package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/MagalixCorp/magalix-agent/v2/admission/target"
	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	opa "github.com/open-policy-agent/frameworks/constraint/pkg/client"
	"github.com/open-policy-agent/frameworks/constraint/pkg/types"
	"golang.org/x/sync/errgroup"
	v1 "k8s.io/api/admission/v1"
	"k8s.io/api/admission/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

var universalDeserializer = serializer.NewCodecFactory(runtime.NewScheme()).UniversalDeserializer()

type WebHookHandler struct {
	opa  *opa.Client
	kube *kuber.Kube
}

func NewWebHookHandler(opaClient *opa.Client, kube *kuber.Kube) WebHookHandler {
	return WebHookHandler{opa: opaClient, kube: kube}
}

func (wh WebHookHandler) Review(ctx context.Context, request v1beta1.AdmissionRequest) (*types.Responses, error) {
	review := &target.AugmentedReview{AdmissionRequest: &request}
	if request.Namespace != "" {
		namespace, err := wh.kube.GetNamespace(request.Namespace)
		if err != nil {
			return nil, err
		}
		review.Namespace = namespace
	}
	return wh.opa.Review(ctx, review)
}

func writeResponse(writer http.ResponseWriter, v interface{}, status int) {
	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	err := enc.Encode(v)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		msg := fmt.Sprintf("Error while writing response, error: %w", err)
		writer.Write([]byte(msg))
	}
	writer.WriteHeader(status)
	writer.Write(buf.Bytes())
}

func (wh WebHookHandler) HandleReq(writer http.ResponseWriter, request *http.Request) {
	body, err := ioutil.ReadAll(request.Body)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		writer.Write([]byte("Unexpected error while reading request body"))
		return
	}
	var admissionReview v1beta1.AdmissionReview
	_, _, err = universalDeserializer.Decode(body, nil, &admissionReview)

	if err != nil || admissionReview.Request == nil {
		writer.WriteHeader(http.StatusInternalServerError)
		writer.Write([]byte("Received incorrect admission request"))
		return
	}

	admissionReviewResponse := v1.AdmissionReview{
		Response: &v1.AdmissionResponse{
			UID: admissionReview.Request.UID,
		},
	}
	admissionReviewResponse.APIVersion = admissionReview.APIVersion
	admissionReviewResponse.Kind = "AdmissionReview"

	namespace := admissionReview.Request.Namespace
	if namespace == metav1.NamespacePublic || namespace == metav1.NamespaceSystem {
		admissionReviewResponse.Response.Allowed = true
		writeResponse(writer, admissionReviewResponse, http.StatusOK)
		return
	}

	resp, err := wh.Review(request.Context(), *admissionReview.Request)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		writer.Write([]byte(fmt.Sprintf("Error while reviewing request, error: %s", err)))
	}
	violations := resp.Results()

	if len(violations) == 0 {
		admissionReviewResponse.Response.Allowed = true
	} else {
		var responses []string
		for _, violation := range violations {
			responses = append(responses, fmt.Sprintf("[denied by %s] %s", violation.Constraint.GetName(), violation.Msg))
		}
		admissionReviewResponse.Response.Allowed = false
		admissionReviewResponse.Response.Result = &metav1.Status{
			Message: strings.Join(responses, "\n"),
		}

	}
	writeResponse(writer, admissionReviewResponse, http.StatusOK)
}

func (ah *WebHookHandler) Start(ctx context.Context) error {
	eg, _ := errgroup.WithContext(ctx)
	eg.Go(func() error {
		http.HandleFunc("/admission", ah.HandleReq)
		return http.ListenAndServeTLS(":443", "tls.crt", "tls.key", nil)
	})
	return eg.Wait()
}
