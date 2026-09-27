package main

import (
	"crypto/tls"
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
	"github.com/mahdidarabi/KArkive/internal/config"
	"github.com/mahdidarabi/KArkive/internal/controller"
	kmetrics "github.com/mahdidarabi/KArkive/internal/metrics"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(karkivev1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool
	var secureMetrics bool
	var enableHTTP2 bool
	var enableWebhooks bool
	cfg := config.Config{}

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metrics endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", false, "Serve metrics via HTTPS.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false, "Enable HTTP/2 for metrics and webhook servers.")
	flag.BoolVar(&enableWebhooks, "webhooks", false, "Register validating admission webhooks (requires serving certs).")
	flag.StringVar(&cfg.BusyBoxImage, "busybox-image", config.DefaultBusyBoxImage, "Default image for cleanup and compress (find, gzip).")
	flag.StringVar(&cfg.GnuPGImage, "gnupg-image", config.DefaultGnuPGImage, "Default image for encrypt (gpg).")
	flag.StringVar(&cfg.PostgresImage, "postgres-image", config.DefaultPostgresImage, "Default image for pgdump (pg_dump / psql).")
	flag.StringVar(&cfg.McImage, "mc-image", config.DefaultMcImage, "Default minio/mc image for s3-sync.")
	flag.StringVar(&cfg.MariaDBImage, "mariadb-image", config.DefaultMariaDBImage, "Default MariaDB image for mysqldump / mysql restore.")
	flag.StringVar(&cfg.RedisImage, "redis-image", config.DefaultRedisImage, "Default Redis image for redis-cli dump / restore.")
	flag.StringVar(&cfg.TarImage, "tar-image", config.DefaultTarImage, "Default GNU tar image for PVC archives (engine pvc).")
	flag.StringVar(&cfg.DefaultS3Endpoint, "default-s3-endpoint", "", "Fallback S3 endpoint when Backup.spec.s3.endpoint is empty.")
	flag.StringVar(&cfg.DefaultS3Bucket, "default-s3-bucket", "", "Fallback S3 bucket when Backup.spec.s3.bucket is empty.")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}
	tlsOpts := []func(*tls.Config){}
	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	metricsOpts := metricsserver.Options{BindAddress: metricsAddr}
	if secureMetrics {
		metricsOpts.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	mgrOpts := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsOpts,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "karkive.io",
	}
	if enableWebhooks {
		mgrOpts.WebhookServer = webhook.NewServer(webhook.Options{
			TLSOpts: tlsOpts,
		})
	}

	if ns := os.Getenv("WATCH_NAMESPACE"); ns != "" {
		mgrOpts.Cache = cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				ns: {},
			},
		}
		setupLog.Info("watching a single namespace", "namespace", ns)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), mgrOpts)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err := (&controller.BackupReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("backup-controller"),
		Config:   cfg,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Backup")
		os.Exit(1)
	}

	if err := (&controller.RestoreReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("restore-controller"),
		Config:   cfg,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Restore")
		os.Exit(1)
	}

	if enableWebhooks {
		if err := (&karkivev1alpha1.Backup{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "Backup")
			os.Exit(1)
		}
		if err := (&karkivev1alpha1.Restore{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "Restore")
			os.Exit(1)
		}
	}

	kmetrics.Register(mgr.GetClient())

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
