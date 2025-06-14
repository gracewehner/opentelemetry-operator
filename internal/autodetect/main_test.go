// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package autodetect_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	kubeTesting "k8s.io/client-go/testing"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/open-telemetry/opentelemetry-operator/internal/autodetect"
	"github.com/open-telemetry/opentelemetry-operator/internal/autodetect/autodetectutils"
	"github.com/open-telemetry/opentelemetry-operator/internal/autodetect/certmanager"
	"github.com/open-telemetry/opentelemetry-operator/internal/autodetect/collector"
	"github.com/open-telemetry/opentelemetry-operator/internal/autodetect/openshift"
	"github.com/open-telemetry/opentelemetry-operator/internal/autodetect/prometheus"
	autoRBAC "github.com/open-telemetry/opentelemetry-operator/internal/autodetect/rbac"
	"github.com/open-telemetry/opentelemetry-operator/internal/autodetect/targetallocator"
	"github.com/open-telemetry/opentelemetry-operator/internal/config"
	"github.com/open-telemetry/opentelemetry-operator/internal/rbac"
)

func TestDetectPlatformBasedOnAvailableAPIGroups(t *testing.T) {
	for _, tt := range []struct {
		apiGroupList *metav1.APIGroupList
		expected     openshift.RoutesAvailability
	}{
		{
			&metav1.APIGroupList{},
			openshift.RoutesNotAvailable,
		},
		{
			&metav1.APIGroupList{
				Groups: []metav1.APIGroup{
					{
						Name: "route.openshift.io",
					},
				},
			},
			openshift.RoutesAvailable,
		},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			output, err := json.Marshal(tt.apiGroupList)
			require.NoError(t, err)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err = w.Write(output)
			require.NoError(t, err)
		}))
		defer server.Close()

		autoDetect, err := autodetect.New(&rest.Config{Host: server.URL}, nil)
		require.NoError(t, err)

		// test
		ora, err := autoDetect.OpenShiftRoutesAvailability()

		// verify
		assert.NoError(t, err)
		assert.Equal(t, tt.expected, ora)
	}
}

func TestDetectPlatformBasedOnAvailableAPIGroupsPrometheus(t *testing.T) {
	for _, tt := range []struct {
		apiGroupList *metav1.APIGroupList
		resources    *metav1.APIResourceList
		expected     prometheus.Availability
	}{
		{
			&metav1.APIGroupList{},
			&metav1.APIResourceList{},
			prometheus.NotAvailable,
		},
		{
			&metav1.APIGroupList{
				Groups: []metav1.APIGroup{
					{
						Name:     "monitoring.coreos.com",
						Versions: []metav1.GroupVersionForDiscovery{{GroupVersion: "monitoring.coreos.com/v1"}},
					},
				},
			},
			&metav1.APIResourceList{
				APIResources: []metav1.APIResource{{Kind: "ServiceMonitor"}},
			},
			prometheus.NotAvailable,
		},
		{
			&metav1.APIGroupList{
				Groups: []metav1.APIGroup{
					{
						Name:     "monitoring.coreos.com",
						Versions: []metav1.GroupVersionForDiscovery{{GroupVersion: "monitoring.coreos.com/v1"}},
					},
				},
			},
			&metav1.APIResourceList{
				APIResources: []metav1.APIResource{{Kind: "PodMonitor"}},
			},
			prometheus.NotAvailable,
		},
		{
			&metav1.APIGroupList{
				Groups: []metav1.APIGroup{
					{
						Name:     "monitoring.coreos.com",
						Versions: []metav1.GroupVersionForDiscovery{{GroupVersion: "monitoring.coreos.com/v1"}},
					},
				},
			},
			&metav1.APIResourceList{
				APIResources: []metav1.APIResource{{Kind: "PodMonitor"}, {Kind: "ServiceMonitor"}},
			},
			prometheus.Available,
		},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			var output []byte
			var err error
			if req.URL.Path == "/apis" {
				output, err = json.Marshal(tt.apiGroupList)
			} else {
				output, err = json.Marshal(tt.resources)
			}
			require.NoError(t, err)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err = w.Write(output)
			require.NoError(t, err)
		}))
		defer server.Close()

		autoDetect, err := autodetect.New(&rest.Config{Host: server.URL}, nil)
		require.NoError(t, err)

		// test
		ora, err := autoDetect.PrometheusCRsAvailability()

		// verify
		assert.NoError(t, err)
		assert.Equal(t, tt.expected, ora)
	}
}

type fakeClientGenerator func() kubernetes.Interface

const (
	createVerb  = "create"
	sarResource = "subjectaccessreviews"
)

func reactorFactory(status v1.SubjectAccessReviewStatus) fakeClientGenerator {
	return func() kubernetes.Interface {
		c := fake.NewSimpleClientset()
		c.PrependReactor(createVerb, sarResource, func(action kubeTesting.Action) (handled bool, ret runtime.Object, err error) {
			// check our expectation here
			if !action.Matches(createVerb, sarResource) {
				return false, nil, fmt.Errorf("must be a create for a SAR")
			}
			sar, ok := action.(kubeTesting.CreateAction).GetObject().DeepCopyObject().(*v1.SubjectAccessReview)
			if !ok || sar == nil {
				return false, nil, fmt.Errorf("bad object")
			}
			sar.Status = status
			return true, sar, nil
		})
		return c
	}
}

func TestDetectRBACPermissionsBasedOnAvailableClusterRoles(t *testing.T) {

	for _, tt := range []struct {
		description          string
		expectedAvailability autoRBAC.Availability
		shouldError          bool
		namespace            string
		serviceAccount       string
		clientGenerator      fakeClientGenerator
	}{
		{
			description:          "Not possible to read the namespace",
			namespace:            "default",
			shouldError:          true,
			expectedAvailability: autoRBAC.NotAvailable,
			clientGenerator: reactorFactory(v1.SubjectAccessReviewStatus{
				Allowed: true,
			}),
		},
		{
			description:    "Not possible to read the service account",
			serviceAccount: "default",
			shouldError:    true,
			clientGenerator: reactorFactory(v1.SubjectAccessReviewStatus{
				Allowed: true,
			}),
		},
		{
			description: "RBAC resources are NOT there",

			shouldError:    true,
			namespace:      "default",
			serviceAccount: "defaultSA",
			clientGenerator: reactorFactory(v1.SubjectAccessReviewStatus{
				Allowed: false,
			}),
			expectedAvailability: autoRBAC.NotAvailable,
		},
		{
			description: "RBAC resources are there",

			shouldError:    false,
			namespace:      "default",
			serviceAccount: "defaultSA",
			clientGenerator: reactorFactory(v1.SubjectAccessReviewStatus{
				Allowed: true,
			}),
			expectedAvailability: autoRBAC.Available,
		},
	} {
		t.Run(tt.description, func(t *testing.T) {
			// These settings can be gotten from env vars
			t.Setenv(autodetectutils.NAMESPACE_ENV_VAR, tt.namespace)
			t.Setenv(autodetectutils.SA_ENV_VAR, tt.serviceAccount)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {}))
			defer server.Close()

			r := rbac.NewReviewer(tt.clientGenerator())

			aD, err := autodetect.New(&rest.Config{Host: server.URL}, r)
			require.NoError(t, err)

			// test
			rAuto, err := aD.RBACPermissions(context.Background())

			// verify
			assert.Equal(t, tt.expectedAvailability, rAuto)
			if tt.shouldError {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCertManagerAvailability(t *testing.T) {
	// test data
	for _, tt := range []struct {
		description          string
		apiGroupList         *metav1.APIGroupList
		expectedAvailability certmanager.Availability
		namespace            string
		serviceAccount       string
		clientGenerator      fakeClientGenerator
		shouldError          bool
	}{
		{
			description:          "CertManager is not installed",
			namespace:            "default",
			serviceAccount:       "defaultSA",
			apiGroupList:         &metav1.APIGroupList{},
			expectedAvailability: certmanager.NotAvailable,
			clientGenerator: reactorFactory(v1.SubjectAccessReviewStatus{
				Allowed: true,
			}),
			shouldError: false,
		},
		{
			description:    "CertManager is installed but RBAC permissions are not granted",
			namespace:      "default",
			serviceAccount: "defaultSA",
			apiGroupList: &metav1.APIGroupList{
				Groups: []metav1.APIGroup{
					{
						Name: "cert-manager.io",
					},
				},
			},
			expectedAvailability: certmanager.NotAvailable,
			clientGenerator: reactorFactory(v1.SubjectAccessReviewStatus{
				Allowed: false,
			}),
			shouldError: true,
		},
		{
			description:    "CertManager is installed and RBAC permissions are granted",
			namespace:      "default",
			serviceAccount: "defaultSA",
			apiGroupList: &metav1.APIGroupList{
				Groups: []metav1.APIGroup{
					{
						Name: "cert-manager.io",
					},
				},
			},
			expectedAvailability: certmanager.Available,
			clientGenerator: reactorFactory(v1.SubjectAccessReviewStatus{
				Allowed: true,
			}),
			shouldError: false,
		},
	} {
		t.Run(tt.description, func(t *testing.T) {
			t.Setenv(autodetectutils.NAMESPACE_ENV_VAR, tt.namespace)
			t.Setenv(autodetectutils.SA_ENV_VAR, tt.serviceAccount)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				output, err := json.Marshal(tt.apiGroupList)
				require.NoError(t, err)

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, err = w.Write(output)
				require.NoError(t, err)
			}))
			defer server.Close()

			r := rbac.NewReviewer(tt.clientGenerator())

			aD, err := autodetect.New(&rest.Config{Host: server.URL}, r)
			require.NoError(t, err)

			// test
			cma, err := aD.CertManagerAvailability(context.Background())

			// verify
			assert.Equal(t, tt.expectedAvailability, cma)
			if tt.shouldError {
				require.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestConfigChangesOnAutoDetect(t *testing.T) {
	// prepare
	mock := &mockAutoDetect{
		OpenShiftRoutesAvailabilityFunc: func() (openshift.RoutesAvailability, error) {
			return openshift.RoutesAvailable, nil
		},
		PrometheusCRsAvailabilityFunc: func() (prometheus.Availability, error) {
			return prometheus.Available, nil
		},
		RBACPermissionsFunc: func(ctx context.Context) (autoRBAC.Availability, error) {
			return autoRBAC.Available, nil
		},
		CertManagerAvailabilityFunc: func(ctx context.Context) (certmanager.Availability, error) {
			return certmanager.Available, nil
		},
		TargetAllocatorAvailabilityFunc: func() (targetallocator.Availability, error) {
			return targetallocator.Available, nil
		},
	}
	cfg := config.New()

	// sanity check
	require.Equal(t, openshift.RoutesNotAvailable, cfg.OpenShiftRoutesAvailability)
	require.Equal(t, prometheus.NotAvailable, cfg.PrometheusCRAvailability)
	require.Equal(t, autoRBAC.NotAvailable, cfg.CreateRBACPermissions)
	require.Equal(t, certmanager.NotAvailable, cfg.CertManagerAvailability)
	require.Equal(t, targetallocator.NotAvailable, cfg.TargetAllocatorAvailability)

	// test
	err := autodetect.ApplyAutoDetect(mock, &cfg, ctrl.Log.WithName("test"))
	require.NoError(t, err)

	// verify
	assert.Equal(t, openshift.RoutesAvailable, cfg.OpenShiftRoutesAvailability)
	require.Equal(t, prometheus.Available, cfg.PrometheusCRAvailability)
	require.Equal(t, autoRBAC.Available, cfg.CreateRBACPermissions)
	require.Equal(t, certmanager.Available, cfg.CertManagerAvailability)
	require.Equal(t, targetallocator.Available, cfg.TargetAllocatorAvailability)
}

var _ autodetect.AutoDetect = (*mockAutoDetect)(nil)

type mockAutoDetect struct {
	OpenShiftRoutesAvailabilityFunc func() (openshift.RoutesAvailability, error)
	PrometheusCRsAvailabilityFunc   func() (prometheus.Availability, error)
	RBACPermissionsFunc             func(ctx context.Context) (autoRBAC.Availability, error)
	CertManagerAvailabilityFunc     func(ctx context.Context) (certmanager.Availability, error)
	TargetAllocatorAvailabilityFunc func() (targetallocator.Availability, error)
	CollectorAvailabilityFunc       func() (collector.Availability, error)
}

func (m *mockAutoDetect) CollectorAvailability() (collector.Availability, error) {
	if m.CollectorAvailabilityFunc != nil {
		return m.CollectorAvailabilityFunc()
	}
	return collector.NotAvailable, nil
}

func (m *mockAutoDetect) FIPSEnabled(_ context.Context) bool {
	return false
}

func (m *mockAutoDetect) OpenShiftRoutesAvailability() (openshift.RoutesAvailability, error) {
	if m.OpenShiftRoutesAvailabilityFunc != nil {
		return m.OpenShiftRoutesAvailabilityFunc()
	}
	return openshift.RoutesNotAvailable, nil
}

func (m *mockAutoDetect) PrometheusCRsAvailability() (prometheus.Availability, error) {
	if m.PrometheusCRsAvailabilityFunc != nil {
		return m.PrometheusCRsAvailabilityFunc()
	}
	return prometheus.NotAvailable, nil
}

func (m *mockAutoDetect) RBACPermissions(ctx context.Context) (autoRBAC.Availability, error) {
	if m.RBACPermissionsFunc != nil {
		return m.RBACPermissionsFunc(ctx)
	}
	return autoRBAC.NotAvailable, nil
}

func (m *mockAutoDetect) CertManagerAvailability(ctx context.Context) (certmanager.Availability, error) {
	if m.CertManagerAvailabilityFunc != nil {
		return m.CertManagerAvailabilityFunc(ctx)
	}
	return certmanager.NotAvailable, nil
}

func (m *mockAutoDetect) TargetAllocatorAvailability() (targetallocator.Availability, error) {
	if m.TargetAllocatorAvailabilityFunc != nil {
		return m.TargetAllocatorAvailabilityFunc()
	}
	return targetallocator.NotAvailable, nil
}
