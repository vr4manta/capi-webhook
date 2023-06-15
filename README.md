# CAPI Machine Webhook

This webhook helps manage machines getting created via the NodePools CRD.  There are some issues with the upstream cluster api that are preventing the Machine API Operator (MAO) from being able to auto accept the CSRs related to the machines being created in the hosted cluster.  This project is a workaround for this.  Currently this webhook will do the following:

- Add InternalDNS entry into the status field of the machine.cluster.x-k8s.io object
- Add NodeRef into the status field when node is created in the hosted cluster

# Installing webhook

```shell
oc create -f manifests/namespace.yaml
oc create -f manifests/rbac.yaml
oc create -f manifests/webhook_service.yaml
oc create -f manifests/deployment.yaml
oc create -f manifests/mutatingwebhook.yaml
```