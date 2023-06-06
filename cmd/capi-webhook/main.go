package main

import (
	"flag"
	"fmt"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/config/leaderelection"
	"github.com/vr4manta/capi-webhook/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"log"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/vr4manta/capi-webhook/pkg/version"
	capiwebhooks "github.com/vr4manta/capi-webhook/pkg/webhook"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	defaultWebhookEnabled          = true
	defaultWebhookPort             = 8443
	defaultWebhookCertdir          = "/etc/capi-webhook/tls"
	DefaultMachineMutatingHookPath = "/mutate-capi-machine"
)

func main() {
	klog.Info("Starting webhook")

	var printVersion bool
	flag.BoolVar(&printVersion, "version", false, "print version and exit")

	// Used to get the default values for leader election from library-go
	defaultLeaderElectionValues := leaderelection.LeaderElectionDefaulting(
		configv1.LeaderElection{},
		"", "",
	)

	klog.InitFlags(nil)
	watchNamespace := flag.String(
		"namespace",
		"",
		"Namespace that the controller watches to reconcile machine-api objects. If unspecified, the controller watches for machine-api objects across all namespaces.",
	)

	webhookEnabled := flag.Bool(
		"webhook-enabled",
		defaultWebhookEnabled,
		"Webhook server, enabled by default. When enabled, the manager will run a webhook server.")

	webhookPort := flag.Int(
		"webhook-port",
		defaultWebhookPort,
		"Webhook Server port, only used when webhook-enabled is true.")

	webhookCertdir := flag.String(
		"webhook-cert-dir",
		defaultWebhookCertdir,
		"Webhook cert dir, only used when webhook-enabled is true.")

	leaderElectResourceNamespace := flag.String(
		"leader-elect-resource-namespace",
		"",
		"The namespace of resource object that is used for locking during leader election. If unspecified and running in cluster, defaults to the service account namespace for the controller. Required for leader-election outside of a cluster.",
	)

	leaderElect := flag.Bool(
		"leader-elect",
		false,
		"Start a leader election client and gain leadership before executing the main loop. Enable this when running replicated components for high availability.",
	)

	// Default values are printed for the user to see, but zero is set as the default to distinguish user intent from default value for topology aware leader election
	leaderElectLeaseDuration := flag.Duration(
		"leader-elect-lease-duration",
		0,
		fmt.Sprintf("The duration that non-leader candidates will wait after observing a leadership renewal until attempting to acquire leadership of a led but unrenewed leader slot. This is effectively the maximum duration that a leader can be stopped before it is replaced by another candidate. This is only applicable if leader election is enabled. Default: (%s)", defaultLeaderElectionValues.LeaseDuration.Duration),
	)

	if err := flag.Set("logtostderr", "true"); err != nil {
		klog.Fatalf("failed to set logtostderr flag: %v", err)
	}

	healthAddr := flag.String(
		"health-addr",
		":9440",
		"The address for health checking.",
	)
	flag.Parse()

	if printVersion {
		fmt.Println(version.String)
		os.Exit(0)
	}

	cfg := config.GetConfigOrDie()
	syncPeriod := 10 * time.Minute

	le := util.GetLeaderElectionConfig(cfg, configv1.LeaderElection{
		Disable:       !*leaderElect,
		LeaseDuration: metav1.Duration{Duration: *leaderElectLeaseDuration},
	})

	klog.Info("Creating opts")
	opts := manager.Options{
		HealthProbeBindAddress:  *healthAddr,
		SyncPeriod:              &syncPeriod,
		LeaderElection:          *leaderElect,
		LeaderElectionNamespace: *leaderElectResourceNamespace,
		LeaderElectionID:        "capi-webhook-leader",
		LeaseDuration:           &le.LeaseDuration.Duration,
		RetryPeriod:             &le.RetryPeriod.Duration,
		RenewDeadline:           &le.RenewDeadline.Duration,
	}

	if *watchNamespace != "" {
		opts.Namespace = *watchNamespace
		klog.Infof("Watching machine-api objects only in namespace %q for reconciliation.", opts.Namespace)
	}

	// Setup a Manager
	klog.Info("Creating manager")
	mgr, err := manager.New(cfg, opts)
	if err != nil {
		klog.Fatalf("Failed to set up overall controller manager: %v", err)
	}

	// Enable defaulting and validating webhooks
	machineDefaulter, err := capiwebhooks.NewMachineDefaulter()
	if err != nil {
		log.Fatal(err)
	}

	if *webhookEnabled {
		mgr.GetWebhookServer().Port = *webhookPort
		mgr.GetWebhookServer().CertDir = *webhookCertdir
		mgr.GetWebhookServer().Register(DefaultMachineMutatingHookPath, &webhook.Admission{Handler: machineDefaulter})
		/*mgr.GetWebhookServer().Register(mapiwebhooks.DefaultMachineValidatingHookPath, &webhook.Admission{Handler: machineValidator})
		mgr.GetWebhookServer().Register(mapiwebhooks.DefaultMachineSetMutatingHookPath, &webhook.Admission{Handler: machineSetDefaulter})
		mgr.GetWebhookServer().Register(mapiwebhooks.DefaultMachineSetValidatingHookPath, &webhook.Admission{Handler: machineSetValidator})*/
	}

	log.Printf("Registering Components.")

	// Setup Scheme for all resources
	if err := clusterv1.AddToScheme(mgr.GetScheme()); err != nil {
		log.Fatal(err)
	}

	klog.Info("Adding readyz")
	if err := mgr.AddReadyzCheck("ping", healthz.Ping); err != nil {
		klog.Fatal(err)
	}

	klog.Info("Adding healthz")
	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		klog.Fatal(err)
	}

	klog.Info("Starting manager")
	if err = mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		klog.Fatalf("Failed to run manager: %v", err)
	}
}
