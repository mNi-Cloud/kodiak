/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package e2e

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mNi-Cloud/kodiak/test/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const namespace = "kodiak-system"

var _ = Describe("Kodiak v0.2", Ordered, func() {
	var controllerPodName string

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	BeforeAll(func() {
		_, err := utils.Run(exec.Command("kubectl", "create", "namespace", namespace))
		Expect(err).NotTo(HaveOccurred())
		_, err = utils.Run(exec.Command("make", "install"))
		Expect(err).NotTo(HaveOccurred())
		_, err = utils.Run(exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage)))
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		_, _ = utils.Run(exec.Command("make", "undeploy"))
		_, _ = utils.Run(exec.Command("make", "uninstall"))
		_, _ = utils.Run(exec.Command("kubectl", "delete", "namespace", namespace, "--ignore-not-found"))
	})

	AfterEach(func() {
		if !CurrentSpecReport().Failed() || controllerPodName == "" {
			return
		}
		output, _ := utils.Run(exec.Command("kubectl", "logs", controllerPodName, "-n", namespace))
		_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n%s\n", output)
	})

	It("starts the controller manager", func() {
		Eventually(func() error {
			output, err := utils.Run(exec.Command(
				"kubectl", "get", "pods",
				"-n", namespace,
				"-l", "control-plane=controller-manager",
				"-o", "jsonpath={.items[0].metadata.name}:{.items[0].status.phase}",
			))
			if err != nil {
				return err
			}
			name, phase, found := strings.Cut(output, ":")
			if !found || phase != "Running" {
				return fmt.Errorf("manager is not running: %q", output)
			}
			controllerPodName = name
			return nil
		}).Should(Succeed())

	})

	It("reconciles an external Connector into stable kernel-networking resources", func() {
		manifest := `
apiVersion: v1
kind: Secret
metadata:
  name: e2e-auth
  namespace: default
stringData:
  TS_AUTH_KEY: tskey-auth-e2e
---
apiVersion: kodiak.mnicloud.jp/v1alpha1
kind: Connector
metadata:
  name: e2e
  namespace: default
spec:
  authKeySecretRef:
    name: e2e-auth
    key: TS_AUTH_KEY
  loginURL: https://control.example.test
  subnetRouter:
    advertiseRoutes:
      - 10.0.1.0/24
`
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		defer func() {
			_, _ = utils.Run(exec.Command("kubectl", "delete", "connector", "e2e", "-n", "default", "--wait=false"))
			_, _ = utils.Run(exec.Command("kubectl", "delete", "secret", "e2e-auth", "-n", "default"))
		}()

		Eventually(func() error {
			_, err := utils.Run(exec.Command("kubectl", "get", "statefulset", "e2e-connector", "-n", "default"))
			return err
		}).Should(Succeed())
		_, err = utils.Run(exec.Command("kubectl", "get", "secret", "e2e-connector-0", "-n", "default"))
		Expect(err).NotTo(HaveOccurred())

		output, err := utils.Run(exec.Command(
			"kubectl", "get", "statefulset", "e2e-connector", "-n", "default",
			"-o", "jsonpath={.spec.template.spec.containers[0].env[?(@.name=='TS_USERSPACE')].value}",
		))
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(Equal("false"))
	})

	It("enforces the v0.2 Connector source union in the API server", func() {
		invalid := `
apiVersion: kodiak.mnicloud.jp/v1alpha1
kind: Connector
metadata:
  name: invalid
  namespace: default
spec:
  subnetRouter:
    advertiseRoutes:
      - 10.0.1.0/24
`
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(invalid)
		output, err := utils.Run(cmd)
		Expect(err).To(HaveOccurred())
		Expect(output).To(ContainSubstring("exactly one of tailnetRef or authKeySecretRef"))
	})
})
