// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package autodetect is for auto-detecting traits from the environment (platform, APIs, ...).
package autodetect

import (
	"context"
	"fmt"
	"slices"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"

	"github.com/open-telemetry/opentelemetry-operator/internal/autodetect/certmanager"
	"github.com/open-telemetry/opentelemetry-operator/internal/autodetect/collector"
	"github.com/open-telemetry/opentelemetry-operator/internal/autodetect/fips"
	"github.com/open-telemetry/opentelemetry-operator/internal/autodetect/openshift"
	"github.com/open-telemetry/opentelemetry-operator/internal/autodetect/prometheus"
	autoRBAC "github.com/open-telemetry/opentelemetry-operator/internal/autodetect/rbac"
	"github.com/open-telemetry/opentelemetry-operator/internal/autodetect/targetallocator"
	"github.com/open-telemetry/opentelemetry-operator/internal/config"
	"github.com/open-telemetry/opentelemetry-operator/internal/rbac"
)

var _ AutoDetect = (*autoDetect)(nil)

// AutoDetect provides an assortment of routines that auto-detect traits based on the runtime.
type AutoDetect interface {
	OpenShiftRoutesAvailability() (openshift.RoutesAvailability, error)
	PrometheusCRsAvailability() (prometheus.Availability, error)
	RBACPermissions(ctx context.Context) (autoRBAC.Availability, error)
	CertManagerAvailability(ctx context.Context) (certmanager.Availability, error)
	TargetAllocatorAvailability() (targetallocator.Availability, error)
	CollectorAvailability() (collector.Availability, error)
	FIPSEnabled(ctx context.Context) bool
}

type autoDetect struct {
	dcl      discovery.DiscoveryInterface
	reviewer *rbac.Reviewer
}

// New creates a new auto-detection worker, using the given client when talking to the current cluster.
func New(restConfig *rest.Config, reviewer *rbac.Reviewer) (AutoDetect, error) {
	dcl, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		// it's pretty much impossible to get into this problem, as most of the
		// code branches from the previous call just won't fail at all,
		// but let's handle this error anyway...
		return nil, err
	}

	return &autoDetect{
		dcl:      dcl,
		reviewer: reviewer,
	}, nil
}

// PrometheusCRsAvailability checks if Prometheus CRDs are available.
func (a *autoDetect) PrometheusCRsAvailability() (prometheus.Availability, error) {
	apiList, err := a.dcl.ServerGroups()
	if err != nil {
		return prometheus.NotAvailable, err
	}

	foundServiceMonitor := false
	foundPodMonitor := false
	apiGroups := apiList.Groups
	for i := 0; i < len(apiGroups); i++ {
		if apiGroups[i].Name == "monitoring.coreos.com" {
			for _, version := range apiGroups[i].Versions {
				resources, err := a.dcl.ServerResourcesForGroupVersion(version.GroupVersion)
				if err != nil {
					return prometheus.NotAvailable, err
				}

				for _, resource := range resources.APIResources {
					if resource.Kind == "ServiceMonitor" {
						foundServiceMonitor = true
					} else if resource.Kind == "PodMonitor" {
						foundPodMonitor = true
					}
				}
			}
		}
	}

	if foundServiceMonitor && foundPodMonitor {
		return prometheus.Available, nil
	}

	return prometheus.NotAvailable, nil
}

// OpenShiftRoutesAvailability checks if OpenShift Route are available.
func (a *autoDetect) OpenShiftRoutesAvailability() (openshift.RoutesAvailability, error) {
	apiList, err := a.dcl.ServerGroups()
	if err != nil {
		return openshift.RoutesNotAvailable, err
	}

	apiGroups := apiList.Groups
	for i := 0; i < len(apiGroups); i++ {
		if apiGroups[i].Name == "route.openshift.io" {
			return openshift.RoutesAvailable, nil
		}
	}

	return openshift.RoutesNotAvailable, nil
}

func (a *autoDetect) RBACPermissions(ctx context.Context) (autoRBAC.Availability, error) {
	w, err := autoRBAC.CheckRBACPermissions(ctx, a.reviewer)
	if err != nil {
		return autoRBAC.NotAvailable, err
	}
	if w != nil {
		return autoRBAC.NotAvailable, fmt.Errorf("missing permissions: %s", w)
	}

	return autoRBAC.Available, nil
}

func (a *autoDetect) CertManagerAvailability(ctx context.Context) (certmanager.Availability, error) {
	apiList, err := a.dcl.ServerGroups()
	if err != nil {
		return certmanager.NotAvailable, err
	}

	apiGroups := apiList.Groups
	certManagerFound := false
	for i := 0; i < len(apiGroups); i++ {
		if apiGroups[i].Name == "cert-manager.io" {
			certManagerFound = true
			break
		}
	}

	if !certManagerFound {
		return certmanager.NotAvailable, nil
	}

	w, err := certmanager.CheckCertManagerPermissions(ctx, a.reviewer)
	if err != nil {
		return certmanager.NotAvailable, err
	}
	if w != nil {
		return certmanager.NotAvailable, fmt.Errorf("missing permissions: %s", w)
	}

	return certmanager.Available, nil
}

// TargetAllocatorAvailability checks if TargetAllocator CR are available.
func (a *autoDetect) TargetAllocatorAvailability() (targetallocator.Availability, error) {
	apiList, err := a.dcl.ServerGroups()
	if err != nil {
		return targetallocator.NotAvailable, err
	}

	apiGroups := apiList.Groups
	otelGroupIndex := slices.IndexFunc(apiGroups, func(group metav1.APIGroup) bool {
		return group.Name == "opentelemetry.io"
	})
	if otelGroupIndex == -1 {
		return targetallocator.NotAvailable, nil
	}

	otelGroup := apiGroups[otelGroupIndex]
	for _, groupVersion := range otelGroup.Versions {
		resourceList, err := a.dcl.ServerResourcesForGroupVersion(groupVersion.GroupVersion)
		if err != nil {
			return targetallocator.NotAvailable, err
		}
		targetAllocatorIndex := slices.IndexFunc(resourceList.APIResources, func(group metav1.APIResource) bool {
			return group.Kind == "TargetAllocator"
		})
		if targetAllocatorIndex >= 0 {
			return targetallocator.Available, nil
		}
	}

	return targetallocator.NotAvailable, nil
}

// CollectorAvailability checks if OpenTelemetryCollector CR are available.
func (a *autoDetect) CollectorAvailability() (collector.Availability, error) {
	apiList, err := a.dcl.ServerGroups()
	if err != nil {
		return collector.NotAvailable, err
	}

	apiGroups := apiList.Groups
	otelGroupIndex := slices.IndexFunc(apiGroups, func(group metav1.APIGroup) bool {
		return group.Name == "opentelemetry.io"
	})
	if otelGroupIndex == -1 {
		return collector.NotAvailable, nil
	}

	otelGroup := apiGroups[otelGroupIndex]
	for _, groupVersion := range otelGroup.Versions {
		resourceList, err := a.dcl.ServerResourcesForGroupVersion(groupVersion.GroupVersion)
		if err != nil {
			return collector.NotAvailable, err
		}
		collectorIndex := slices.IndexFunc(resourceList.APIResources, func(group metav1.APIResource) bool {
			return group.Kind == "OpenTelemetryCollector"
		})
		if collectorIndex >= 0 {
			return collector.Available, nil
		}
	}

	return collector.NotAvailable, nil
}

func (a *autoDetect) FIPSEnabled(_ context.Context) bool {
	return fips.IsFipsEnabled()
}

// ApplyAutoDetect attempts to automatically detect relevant information for this operator.
func ApplyAutoDetect(autoDetect AutoDetect, c *config.Config, logger logr.Logger) error {
	logger.V(2).Info("auto-detecting the configuration based on the environment")

	ora, err := autoDetect.OpenShiftRoutesAvailability()
	if err != nil {
		return err
	}
	c.OpenShiftRoutesAvailability = ora
	logger.V(2).Info("openshift routes detected", "availability", ora)

	pcrd, err := autoDetect.PrometheusCRsAvailability()
	if err != nil {
		return err
	}
	c.PrometheusCRAvailability = pcrd
	logger.V(2).Info("prometheus cr detected", "availability", pcrd)

	rAuto, err := autoDetect.RBACPermissions(context.Background())
	if err != nil {
		logger.V(2).Info("the rbac permissions are not set for the operator", "reason", err)
	}
	c.CreateRBACPermissions = rAuto
	logger.V(2).Info("create rbac permissions detected", "availability", rAuto)

	cmAvl, err := autoDetect.CertManagerAvailability(context.Background())
	if err != nil {
		logger.V(2).Info("the cert manager crd and permissions are not set for the operator", "reason", err)
	}
	c.CertManagerAvailability = cmAvl
	logger.V(2).Info("the cert manager crd and permissions are set for the operator", "availability", cmAvl)

	taAvl, err := autoDetect.TargetAllocatorAvailability()
	if err != nil {
		return err
	}
	c.TargetAllocatorAvailability = taAvl
	logger.V(2).Info("determined TargetAllocator CRD availability", "availability", cmAvl)

	coAvl, err := autoDetect.CollectorAvailability()
	if err != nil {
		return err
	}
	c.CollectorAvailability = coAvl
	logger.V(2).Info("determined Collector CRD availability", "availability", coAvl)

	return nil
}
