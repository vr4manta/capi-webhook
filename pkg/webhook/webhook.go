package webhooks

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
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
	cfg    *rest.Config
}

type admissionHandler struct {
	*admissionConfig
	decoder *admission.Decoder
}

type WebhookContext struct {
	handler *machineDefaulterHandler
	context context.Context
	machine *clusterv1.Machine
}

type ClientData struct {
	clientSet *kubernetes.Clientset
	Informer  cache.SharedInformer
	Stop      chan struct{}
}

var (
	clientCache = make(map[string]*ClientData)
)

// InjectDecoder injects the decoder.
func (a *admissionHandler) InjectDecoder(d *admission.Decoder) error {
	klog.Info("Injecting Decoder")
	a.decoder = d
	return nil
}

// NewMachineDefaulter returns a new machineDefaulterHandler.
func NewMachineDefaulter(client client.Client, cfg *rest.Config) (*machineDefaulterHandler, error) {
	return createMachineDefaulter(client, cfg), nil
}

func createMachineDefaulter(client client.Client, cfg *rest.Config) *machineDefaulterHandler {
	return &machineDefaulterHandler{
		admissionHandler: &admissionHandler{
			admissionConfig: &admissionConfig{
				client: client,
				cfg:    cfg,
			},
		},
	}
}

// Handle handles HTTP requests for admission webhook servers.
func (h *machineDefaulterHandler) Handle(ctx context.Context, req admission.Request) admission.Response {
	klog.Infof("Got request to Handle: %v", req.Name)

	if req.Name == "" {
		klog.Infof("Got early webhook notification.  Ignoring.")
	}

	machine := &clusterv1.Machine{}
	if err := h.decoder.Decode(req, machine); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	webhookCtx := WebhookContext{
		handler: h,
		context: ctx,
		machine: machine,
	}

	// Check to see if we have an IP and if so, lets also add the machine DNS hostname if it does not exit.
	dnsUpdated := checkInternalDNS(machine)

	nodeUpdate := checkNodeRef(webhookCtx)

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

func checkNodeRef(webhookCtx WebhookContext) bool { //context context.Context, client client.Client, cfg rest.Config, machine *clusterv1.Machine) bool {
	machine := webhookCtx.machine
	// Check to see if nodeRef is set.
	if machine.Status.NodeRef != nil {
		klog.V(4).Infof("NodeRef is not nil.  Aborting check.")
		return false
	}

	nodeID := machine.Name
	hyperProj := machine.Namespace
	klog.Infof("Checking nodeRef for %v in cluster %v", nodeID, hyperProj)

	node := &corev1.Node{}

	// Create client for hypershift cluster
	clientset, err := getClient(webhookCtx, hyperProj)

	// Get Node info
	node, err = clientset.CoreV1().Nodes().Get(webhookCtx.context, nodeID, metav1.GetOptions{})
	if err != nil {
		// TODO: Should check to make sure error is node not found vs some other error.
		klog.Infof("Node Err: %v", err)
		klog.V(4).Info("Unable to find node.  Starting watcher.")

		// Node not found.  Let's watch it to perform a manual update since we are not sure how often webhook
		// will be called to allow us to check for node creation.
		WatchNode(webhookCtx.handler.client, clientset, hyperProj, nodeID)

		return false
	}

	// If node found, set the status
	if node != nil {
		klog.Infof("Got node: %v", node.Name)
		StopWatchingNode(hyperProj, nodeID)
		machine.Status.NodeRef = &corev1.ObjectReference{
			Kind: "Node",
			Name: nodeID,
			UID:  node.UID,
		}
		return true
	}

	return false
}

func getClient(webhookCtx WebhookContext, cluster string) (*kubernetes.Clientset, error) { //context context.Context, client client.Client, cfg rest.Config, cluster string) (*kubernetes.Clientset, error) {
	// Check client cache
	clientData, ok := clientCache[cluster]
	if ok {
		klog.V(4).Infof("Found cached client.")
		return clientData.clientSet, nil
	}

	// If no client found, create one
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{
		Name:      "admin-kubeconfig",
		Namespace: cluster,
	}

	webClient := webhookCtx.handler.client
	klog.V(4).Infof("Loading secret admin-kubeconfig in namespace %v", cluster)
	err := webClient.Get(webhookCtx.context, secretKey, secret)
	if err != nil {
		klog.Warningf("Unable to get kubeconfig for hypershift cluster: %v", err)
		return nil, err
	}

	// Create listener for secret.  If deleted, then cluster is being torn down, and we need to clean up.
	localClientset, err := kubernetes.NewForConfig(webhookCtx.handler.cfg)
	if err != nil {
		klog.Errorf("Unable to create clientset for secret watch: ", err)
		return nil, err
	}

	kubeInformerFactory := informers.NewSharedInformerFactoryWithOptions(localClientset, time.Second*30, informers.WithNamespace(cluster))
	secretInformer := kubeInformerFactory.Core().V1().Secrets().Informer()
	secretInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		DeleteFunc: secretDeleted,
	})
	stop := make(chan struct{})
	kubeInformerFactory.Start(stop)

	// Create kubeconfig
	kubeconfig := secret.Data["kubeconfig"]
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		klog.Warningf("Unable to load kubeconfig: %v", err)
		return nil, err
	}

	// Create client for hypershift cluster
	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Warningf("Unable to create client: %v", err)
		return nil, err
	}
	clientCache[cluster] = &ClientData{
		clientSet: clientSet,
		Informer:  secretInformer,
		Stop:      stop,
	}
	return clientSet, err
}

func secretDeleted(obj interface{}) {
	klog.V(4).Info("Received secret delete event.")
	// Check if secret is in cache.  If so, lets remove our cached client.
	secret := obj.(*corev1.Secret)
	cluster := secret.Namespace
	if secret.Name == "admin-kubeconfig" {
		clientData := clientCache[cluster]
		klog.V(2).Infof("Secret deleted for admin-kubeconfig of cluster %v.", cluster)
		delete(clientCache, cluster)
		defer close(clientData.Stop)
	}
}
