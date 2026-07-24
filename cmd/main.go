/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"net/url"
	"os"

	kodiakv1alpha1 "github.com/mNi-Cloud/kodiak/api/v1alpha1"
	controlclient "github.com/mNi-Cloud/kodiak/internal/client"
	"github.com/mNi-Cloud/kodiak/internal/controller"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kodiakv1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr          string
		probeAddr            string
		enableLeaderElection bool
		secureMetrics        bool
		enableHTTP2          bool
		ionscaleAPIEndpoint  string
		ionscaleLoginURL     string
		ionscaleAdminKey     string
		ionscaleSkipTLS      bool
		tailscaleImage       string
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "Metrics bind address; 0 disables metrics.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Health probe bind address.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true, "Serve metrics using HTTPS with Kubernetes authentication.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false, "Enable HTTP/2 for the metrics server.")
	flag.StringVar(&ionscaleAPIEndpoint, "ionscale-api-endpoint", "", "Ionscale system administration API endpoint.")
	flag.StringVar(
		&ionscaleLoginURL,
		"ionscale-login-url",
		"",
		"Public Tailscale protocol URL used by managed Connectors.",
	)
	flag.BoolVar(&ionscaleSkipTLS, "ionscale-skip-tls-verify", false, "Skip Ionscale API TLS verification.")
	flag.StringVar(&tailscaleImage, "tailscale-image", "tailscale/tailscale:v1.98.9", "Pinned Tailscale Connector image.")

	zapOptions := zap.Options{Development: true}
	zapOptions.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOptions)))

	if ionscaleAdminKey == "" {
		ionscaleAdminKey = os.Getenv("IONSCALE_ADMIN_KEY")
	}
	if ionscaleAPIEndpoint == "" {
		ionscaleAPIEndpoint = os.Getenv("IONSCALE_API_ENDPOINT")
	}
	if ionscaleLoginURL == "" {
		ionscaleLoginURL = os.Getenv("IONSCALE_LOGIN_URL")
	}
	if ionscaleLoginURL != "" {
		parsed, err := url.Parse(ionscaleLoginURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			if err == nil {
				err = fmt.Errorf("expected https scheme and non-empty host")
			}
			setupLog.Error(err, "ionscale login URL must be an absolute HTTPS URL", "url", ionscaleLoginURL)
			os.Exit(1)
		}
	}

	tlsOptions := []func(*tls.Config){}
	if !enableHTTP2 {
		tlsOptions = append(tlsOptions, func(config *tls.Config) {
			config.NextProtos = []string{"http/1.1"}
		})
	}
	metricsOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOptions,
	}
	if secureMetrics {
		metricsOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	manager, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsOptions,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "c3b1ffec.mnicloud.jp",
	})
	if err != nil {
		setupLog.Error(err, "create manager")
		os.Exit(1)
	}

	clientFactory := controlclient.DefaultClientFactory()
	if err := (&controller.TailnetReconciler{
		Client:           manager.GetClient(),
		Scheme:           manager.GetScheme(),
		ClientFactory:    clientFactory,
		IonscaleEndpoint: ionscaleAPIEndpoint,
		IonscaleLoginURL: ionscaleLoginURL,
		IonscaleAdminKey: ionscaleAdminKey,
		IonscaleSkipTLS:  ionscaleSkipTLS,
	}).SetupWithManager(manager); err != nil {
		setupLog.Error(err, "set up Tailnet controller")
		os.Exit(1)
	}
	if err := (&controller.AuthKeyReconciler{
		Client:           manager.GetClient(),
		Scheme:           manager.GetScheme(),
		ClientFactory:    clientFactory,
		IonscaleEndpoint: ionscaleAPIEndpoint,
		IonscaleAdminKey: ionscaleAdminKey,
		IonscaleSkipTLS:  ionscaleSkipTLS,
	}).SetupWithManager(manager); err != nil {
		setupLog.Error(err, "set up AuthKey controller")
		os.Exit(1)
	}
	if err := (&controller.ConnectorReconciler{
		Client:           manager.GetClient(),
		Scheme:           manager.GetScheme(),
		ClientFactory:    clientFactory,
		IonscaleEndpoint: ionscaleAPIEndpoint,
		IonscaleAdminKey: ionscaleAdminKey,
		IonscaleSkipTLS:  ionscaleSkipTLS,
		TailscaleImage:   tailscaleImage,
	}).SetupWithManager(manager); err != nil {
		setupLog.Error(err, "set up Connector controller")
		os.Exit(1)
	}
	if err := (&controller.ConnectorInstanceReconciler{
		Client:           manager.GetClient(),
		Scheme:           manager.GetScheme(),
		ClientFactory:    clientFactory,
		IonscaleEndpoint: ionscaleAPIEndpoint,
		IonscaleAdminKey: ionscaleAdminKey,
		IonscaleSkipTLS:  ionscaleSkipTLS,
	}).SetupWithManager(manager); err != nil {
		setupLog.Error(err, "set up ConnectorInstance controller")
		os.Exit(1)
	}

	if err := manager.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "add health check")
		os.Exit(1)
	}
	if err := manager.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "add readiness check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := manager.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "run manager")
		os.Exit(1)
	}
}
