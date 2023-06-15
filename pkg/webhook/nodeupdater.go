package webhooks

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type NodeWatcher struct {
	Client    client.Client
	Informer  cache.SharedInformer
	NodeList  []string
	clusterID string
	Stop      chan struct{}
}

var (
	informerMap = make(map[string]*NodeWatcher)
)

func WatchNode(client client.Client, clientset *kubernetes.Clientset, clusterName, nodeName string) {
	klog.Infof("Checking for watcher for %v", clusterName)
	watcher, ok := informerMap[clusterName]
	if !ok {
		klog.Info("Creating watcher")
		watcher = &NodeWatcher{
			Client:    client,
			NodeList:  []string{},
			clusterID: clusterName,
		}

		kubeInformerFactory := informers.NewSharedInformerFactory(clientset, time.Second*30)
		nodeInformer := kubeInformerFactory.Core().V1().Nodes().Informer()

		_, err := nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    watcher.nodeAdded,
			DeleteFunc: watcher.nodeDeleted,
		})
		if err != nil {
			klog.Warningf("Unable to create informer: %v", err)
			return
		}
		watcher.Informer = nodeInformer
		informerMap[clusterName] = watcher

		watcher.Stop = make(chan struct{})
		// TODO: perform stop when shutting down goroutine or when listener list is empty
		kubeInformerFactory.Start(watcher.Stop)
	}

	// If current nodeList does not contain nodeName, add it.
	klog.Infof("Checking to see if node %v is already being watched.", nodeName)
	if !contains(watcher.NodeList, nodeName) {
		klog.Infof("Adding node %v to the watch list", nodeName)
		watcher.NodeList = append(watcher.NodeList, nodeName)
	}
}

func StopWatchingNode(clusterName, nodeName string) {
	klog.V(4).Infof("Attempting to remove node %v from watch list", nodeName)
	watcher, ok := informerMap[clusterName]
	if ok {
		for index, node := range watcher.NodeList {
			if node == nodeName {
				watcher.NodeList[index] = watcher.NodeList[len(watcher.NodeList)-1]
				watcher.NodeList = watcher.NodeList[:len(watcher.NodeList)-1]
				klog.Infof("Node %v has been removed from watch list", nodeName)
				break
			}
		}

		if len(watcher.NodeList) == 0 {
			klog.Infof("Removing watcher")
			delete(informerMap, clusterName)
			defer close(watcher.Stop)
		}
	}
}

func (n *NodeWatcher) nodeAdded(obj interface{}) {
	node := obj.(*v1.Node)
	klog.Infof("Got add event for node %v", node.Name)

	if contains(n.NodeList, node.Name) {
		klog.Info("Received add event for a watched node.")
		machine := &clusterv1.Machine{}
		machineInfo := types.NamespacedName{
			Name:      node.Name,
			Namespace: n.clusterID,
		}

		klog.Infof("Getting machine for node %v", node.Name)
		err := n.Client.Get(context.TODO(), machineInfo, machine)
		if err != nil {
			klog.Errorf("Unable to get machine for node %v: %v", node.Name, err)
			return
		}
		machine.Status.NodeRef = &corev1.ObjectReference{
			Kind: "Node",
			Name: node.Name,
			UID:  node.UID,
		}

		klog.Infof("Updating nodeRef for machine %v", node.Name)
		err = n.Client.Status().Update(context.TODO(), machine)
		if err != nil {
			klog.Errorf("Unable to update machine nodeRef for %v: %v", node.Name, err)
		} else {
			klog.Infof("Update complete.  Removing node from watch list.")
			StopWatchingNode(n.clusterID, node.Name)
		}
	} else {
		klog.Info("Node not currently being watched.")
	}
}

func (n *NodeWatcher) nodeDeleted(obj interface{}) {
	node := obj.(*v1.Node)
	klog.Infof("Got delete event for node %v", node.Name)

	if contains(n.NodeList, node.Name) {
		klog.Info("Received delete event for a watched node.")
		StopWatchingNode(n.clusterID, node.Name)

	} else {
		klog.Info("Node not currently being watched.")
	}
}

func contains(array []string, node string) bool {
	for _, a := range array {
		if a == node {
			return true
		}
	}
	return false
}
