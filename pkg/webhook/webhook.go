package webhooks

import (
	"context"
	"encoding/json"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
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

// NewMachineDefaulter returns a new machineDefaulterHandler.
func NewMachineDefaulter(client client.Client) (*machineDefaulterHandler, error) {
	return createMachineDefaulter(client), nil
}

func createMachineDefaulter(client client.Client) *machineDefaulterHandler {
	return &machineDefaulterHandler{
		admissionHandler: &admissionHandler{
			admissionConfig: &admissionConfig{client: client},
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
	dnsUpdated := checkInternalDNS(machine)

	nodeUpdate := checkNodeRef(ctx, h.client, machine)

	if dnsUpdated || nodeUpdate {
		marshaledMachine, err := json.Marshal(machine)
		if err != nil {
			return admission.Errored(http.StatusInternalServerError, err)
		}
		klog.Infof("Updating machine status.")
		return admission.PatchResponseFromRaw(req.Object.Raw, marshaledMachine)
	}

	return admission.Allowed("HAPPY DAY")
}

func checkInternalDNS(machine *clusterv1.Machine) bool {
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
		return true
	}

	return false
}

func checkNodeRef(context context.Context, client client.Client, machine *clusterv1.Machine) bool {
	nodeID := machine.Name
	hyperProj := machine.Namespace

	node := &corev1.Node{}
	/*nodeKey := types.NamespacedName{
		Name: machine.Name,
	}*/

	// Get kubeconfig secret
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{
		Name:      "admin-kubeconfig",
		Namespace: hyperProj,
	}

	klog.Infof("Loading secret admin-kubeconfig in namespace %v", hyperProj)
	err := client.Get(context, secretKey, secret)
	if err != nil {
		klog.Warningf("Unable to get kubeconfig for hypershift cluster: %v", err)
		return false
	}

	// Create kubeconfig
	kubeconfig := secret.Data["kubeconfig"]
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		klog.Warningf("Unable to load kubeconfig: %v", err)
	}

	// Create client for hypershift cluster
	clientset, err := kubernetes.NewForConfig(config)

	// Get Node info
	node, err = clientset.CoreV1().Nodes().Get(context, nodeID, metav1.GetOptions{})
	if err != nil {
		klog.Infof("Node Err: %v", err)
		klog.Info("Unable to find node.  Update skipped.")
		return false
	}

	// If node found, set the status
	if node != nil {
		klog.Infof("Got node: %v", node.Name)
		machine.Status.NodeRef = &corev1.ObjectReference{
			Kind: "Node",
			Name: nodeID,
			UID:  node.UID,
		}
		return true
	}
	return false
}
