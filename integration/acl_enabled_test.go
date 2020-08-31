package integration

import (
	"net/http"
	"time"

	"github.com/containous/maesh/integration/k3d"
	"github.com/containous/maesh/integration/tool"
	"github.com/containous/maesh/integration/try"
	"github.com/go-check/check"
	"github.com/sirupsen/logrus"
	checker "github.com/vdemeester/shakers"
)

// ACLEnabledSuite.
type ACLEnabledSuite struct {
	logger         logrus.FieldLogger
	cluster        *k3d.Cluster
	toolAuthorized *tool.Tool
	toolForbidden  *tool.Tool
}

func (s *ACLEnabledSuite) SetUpSuite(c *check.C) {
	var err error

	requiredImages := []k3d.DockerImage{
		{Name: "containous/maesh:latest", Local: true},
		{Name: "traefik:v2.3"},
		{Name: "containous/whoami:v1.0.1"},
		{Name: "giantswarm/tiny-tools:3.9"},
	}

	s.logger = logrus.New()
	s.cluster, err = k3d.NewCluster(s.logger, masterURL, k3dClusterName,
		k3d.WithoutTraefik(),
		k3d.WithImages(requiredImages...),
	)
	c.Assert(err, checker.IsNil)

	c.Assert(s.cluster.CreateNamespace(s.logger, maeshNamespace), checker.IsNil)
	c.Assert(s.cluster.CreateNamespace(s.logger, testNamespace), checker.IsNil)

	c.Assert(s.cluster.Apply(s.logger, smiCRDs), checker.IsNil)
	c.Assert(s.cluster.Apply(s.logger, "testdata/tool/tool-authorized.yaml"), checker.IsNil)
	c.Assert(s.cluster.Apply(s.logger, "testdata/tool/tool-forbidden.yaml"), checker.IsNil)
	c.Assert(s.cluster.Apply(s.logger, "testdata/maesh/controller-acl-enabled.yaml"), checker.IsNil)
	c.Assert(s.cluster.Apply(s.logger, "testdata/maesh/proxy.yaml"), checker.IsNil)

	c.Assert(s.cluster.WaitReadyPod("tool-authorized", testNamespace, 60*time.Second), checker.IsNil)
	c.Assert(s.cluster.WaitReadyPod("tool-forbidden", testNamespace, 60*time.Second), checker.IsNil)
	c.Assert(s.cluster.WaitReadyDeployment("maesh-controller", maeshNamespace, 60*time.Second), checker.IsNil)
	c.Assert(s.cluster.WaitReadyDaemonSet("maesh-mesh", maeshNamespace, 60*time.Second), checker.IsNil)

	s.toolAuthorized = tool.New(s.logger, "tool-authorized", testNamespace)
	s.toolForbidden = tool.New(s.logger, "tool-forbidden", testNamespace)
}

func (s *ACLEnabledSuite) TearDownSuite(c *check.C) {
	if s.cluster != nil {
		c.Assert(s.cluster.Stop(s.logger), checker.IsNil)
	}
}

func (s *ACLEnabledSuite) TestHTTPServiceWithTrafficTarget(c *check.C) {
	c.Assert(s.cluster.Apply(s.logger, "testdata/acl_enabled/http"), checker.IsNil)
	defer s.cluster.Delete(s.logger, "testdata/acl_enabled/http")

	s.logger.Infof("Asserting TrafficTarget with no HTTPRouteGroup are enforced")
	s.assertHTTPServiceStatus(c, s.toolAuthorized, "server-http.test.maesh:8080", nil, http.StatusOK)
	s.assertHTTPServiceStatus(c, s.toolForbidden, "server-http.test.maesh:8080", nil, http.StatusForbidden)

	s.logger.Infof("Asserting HTTPRouteGroup path filtering is enforced")
	s.assertHTTPServiceStatus(c, s.toolAuthorized, "server-http-api.test.maesh:8080/api", nil, http.StatusOK)
	s.assertHTTPServiceStatus(c, s.toolAuthorized, "server-http-api.test.maesh:8080", nil, http.StatusForbidden)

	s.logger.Infof("Asserting HTTPRouteGroup header filtering is enforced")
	s.assertHTTPServiceStatus(c, s.toolAuthorized, "server-http-header.test.maesh:8080", map[string]string{"Authorized": "true"}, http.StatusOK)
	s.assertHTTPServiceStatus(c, s.toolAuthorized, "server-http-header.test.maesh:8080", map[string]string{"Authorized": "false"}, http.StatusForbidden)
}

func (s *ACLEnabledSuite) TestHTTPServiceWithTrafficSplit(c *check.C) {
	c.Assert(s.cluster.Apply(s.logger, "testdata/acl_enabled/traffic-split"), checker.IsNil)
	defer s.cluster.Delete(s.logger, "testdata/acl_enabled/traffic-split")

	s.logger.Info("Asserting TrafficTarget is enforced")
	s.assertHTTPServiceStatus(c, s.toolAuthorized, "server-http-split.test.maesh:8080", nil, http.StatusOK)
	s.assertHTTPServiceStatus(c, s.toolForbidden, "server-http-split.test.maesh:8080", nil, http.StatusForbidden)
}

func (s *ACLEnabledSuite) assertHTTPServiceStatus(c *check.C, t *tool.Tool, url string, headers map[string]string, expectedStatus int) {
	s.logger.Infof("Asserting status is %q on %q with headers: %v", http.StatusText(expectedStatus), url, headers)

	err := try.Retry(func() error {
		return t.Curl(url, headers, try.StatusCodeIs(expectedStatus))
	}, 60*time.Second)

	c.Assert(err, checker.IsNil)
}