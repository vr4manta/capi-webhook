package webhooks

import (
	"context"
	"encoding/json"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"
	"net/http"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// machineDefaulterHandler defaults Machine API resources.
// implements type Handler interface.
// https://godoc.org/github.com/kubernetes-sigs/controller-runtime/pkg/webhook/admission#Handler
type machineDefaulterHandler struct {
	*admissionHandler
}

type machineAdmissionFn func(m *machinev1beta1.Machine, config *admissionConfig) (bool, []string, utilerrors.Aggregate)

type admissionConfig struct {
	client client.Client
}

type admissionHandler struct {
	*admissionConfig
	decoder *admission.Decoder
}

// InjectDecoder injects the decoder.
func (a *admissionHandler) InjectDecoder(d *admission.Decoder) error {
	klog.Info("Injecting Decoder")
	a.decoder = d
	return nil
}

// NewDefaulter returns a new machineDefaulterHandler.
func NewMachineDefaulter() (*machineDefaulterHandler, error) {
	return createMachineDefaulter(), nil
}

func createMachineDefaulter() *machineDefaulterHandler {
	return &machineDefaulterHandler{
		admissionHandler: &admissionHandler{
			admissionConfig: &admissionConfig{},
		},
	}
}

// Handle handles HTTP requests for admission webhook servers.
func (h *machineDefaulterHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	klog.Infof("Got request to Handle: %v", req.Name)

	machine := &clusterv1.Machine{}
	if err := h.decoder.Decode(req, machine); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Check to see if we have an IP and if so, lets also add the machine DNS hostname if it does not exit.
	externalFound := false
	dnsFound := false
	klog.Infof("Checking for addresses...")
	for _, address := range machine.Status.Addresses {
		switch address.Type {
		case "ExternalIP":
			externalFound = true
			break
		case "InternalDNS":
			dnsFound = true
			break
		}
	}
	klog.Infof("Results of check: ExternalIP(%v) InternalDNS(%v)", externalFound, dnsFound)

	if externalFound && !dnsFound {
		address := clusterv1.MachineAddress{
			Address: machine.Name,
			Type:    "InternalDNS",
		}
		klog.Infof("Generated InternalDNS: %v", address)
		machine.Status.Addresses = append(machine.Status.Addresses, address)

		marshaledMachine, err := json.Marshal(machine)
		if err != nil {
			return admission.Errored(http.StatusInternalServerError, err)
		}
		return admission.PatchResponseFromRaw(req.Object.Raw, marshaledMachine)
	}

	return admission.Allowed("HAPPY DAY")
}
