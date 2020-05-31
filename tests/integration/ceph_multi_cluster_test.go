/*
Copyright 2016 The Rook Authors. All rights reserved.

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

package integration

import (
	"fmt"
	"testing"
	"time"

	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	"github.com/rook/rook/pkg/daemon/ceph/client"
	"github.com/rook/rook/tests/framework/clients"
	"github.com/rook/rook/tests/framework/installer"
	"github.com/rook/rook/tests/framework/utils"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	localPathPVCmd = "tests/scripts/localPathPV.sh"
)

// *************************************************************
// *** Major scenarios tested by the MultiClusterDeploySuite ***
// Setup
// - Two clusters started in different namespaces via the CRD
// Monitors
// - One mon in each cluster
// OSDs
// - Bluestore running on a raw block device
// Block
// - Create a pool in each cluster
// - Mount/unmount a block device through the dynamic provisioner
// File system
// - Create a file system via the CRD
// Object
// - Create the object store via the CRD
// *************************************************************
func TestCephMultiClusterDeploySuite(t *testing.T) {
	if installer.SkipTestSuite(installer.CephTestSuite) {
		t.Skip()
	}

	s := new(MultiClusterDeploySuite)
	defer func(s *MultiClusterDeploySuite) {
		HandlePanics(recover(), s.op, s.T)
	}(s)
	suite.Run(t, s)
}

type MultiClusterDeploySuite struct {
	suite.Suite
	testClient *clients.TestClient
	k8sh       *utils.K8sHelper
	namespace1 string
	namespace2 string
	op         *MCTestOperations
	poolName   string
	useHelm    bool
}

// Deploy Multiple Rook clusters
func (mrc *MultiClusterDeploySuite) SetupSuite() {
	mrc.poolName = "multi-cluster-pool1"
	mrc.namespace1 = "mrc-n1"
	mrc.namespace2 = "mrc-n2"

	mrc.op, mrc.k8sh = NewMCTestOperations(mrc.T, installer.SystemNamespace(mrc.namespace1), mrc.namespace1, mrc.namespace2, false, true)
	mrc.testClient = clients.CreateTestClient(mrc.k8sh, mrc.op.installer.Manifests)
	mrc.createPools()
}

func (mrc *MultiClusterDeploySuite) AfterTest(suiteName, testName string) {
	mrc.op.installer.CollectOperatorLog(suiteName, testName, mrc.op.systemNamespace)
}

func (mrc *MultiClusterDeploySuite) createPools() {
	// create a test pool in each cluster so that we get some PGs
	logger.Infof("Creating pool %s", mrc.poolName)
	err := mrc.testClient.PoolClient.Create(mrc.poolName, mrc.namespace1, 1)
	require.Nil(mrc.T(), err)
}

func (mrc *MultiClusterDeploySuite) deletePools() {
	// create a test pool in each cluster so that we get some PGs
	logger.Infof("Deleting pool %s", mrc.poolName)
	clusterInfo := client.AdminClusterInfo(mrc.namespace1)
	if err := mrc.testClient.PoolClient.DeletePool(mrc.testClient.BlockClient, clusterInfo, mrc.poolName); err != nil {
		logger.Errorf("failed to delete pool %q. %v", mrc.poolName, err)
	} else {
		logger.Infof("deleted pool %q", mrc.poolName)
	}
}

func (mrc *MultiClusterDeploySuite) TearDownSuite() {
	mrc.deletePools()
	mrc.op.Teardown()
}

// Test to make sure all rook components are installed and Running
func (mrc *MultiClusterDeploySuite) TestInstallingMultipleRookClusters() {
	// Check if Rook cluster 1 is deployed successfully
	targetClusterNamespaces := []string{mrc.namespace1}
	checkIfRookClusterIsInstalled(mrc.Suite, mrc.k8sh, installer.SystemNamespace(mrc.namespace1), targetClusterNamespaces, 1)
	checkIfRookClusterIsHealthy(mrc.Suite, mrc.testClient, mrc.namespace1)

	// Check if Rook external cluster is deployed successfully
	// Checking health status is enough to validate the connection
	checkIfRookClusterIsHealthy(mrc.Suite, mrc.testClient, mrc.namespace2)
}

// MCTestOperations struct for handling panic and test suite tear down
type MCTestOperations struct {
	installer        *installer.CephInstaller
	kh               *utils.K8sHelper
	T                func() *testing.T
	namespace1       string
	namespace2       string
	systemNamespace  string
	storageClassName string
	testOverPVC      bool
	// This value is not necessary
	useHelm         bool
	externalCluster bool
}

// NewMCTestOperations creates new instance of TestCluster struct
func NewMCTestOperations(t func() *testing.T, systemNamespace string, namespace1 string, namespace2 string, useHelm bool, useExternalCluster bool) (*MCTestOperations, *utils.K8sHelper) {

	kh, err := utils.CreateK8sHelper(t)
	require.NoError(t(), err)
	checkIfShouldRunForMinimalTestMatrix(t, kh, multiClusterMinimalTestVersion)

	cleanupHost := false
	i := installer.NewCephInstaller(t, kh.Clientset, useHelm, "", installer.VersionMaster, installer.NautilusVersion, cleanupHost)

	op := &MCTestOperations{
		installer:        i,
		kh:               kh,
		T:                t,
		namespace1:       namespace1,
		namespace2:       namespace2,
		systemNamespace:  systemNamespace,
		storageClassName: "",
		testOverPVC:      false,
		useHelm:          useHelm,
		externalCluster:  useExternalCluster,
	}

	if kh.VersionAtLeast("v1.13.0") {
		op.testOverPVC = true
		op.storageClassName = "manual"
	}
	op.Setup()
	return op, kh
}

// Setup is wrapper for setting up multiple rook clusters.
func (o MCTestOperations) Setup() {
	var err error
	if o.testOverPVC {
		cmdArgs := utils.CommandArgs{Command: localPathPVCmd, CmdArgs: []string{installer.TestScratchDevice(), installer.TestScratchDevice2()}}
		cmdOut := utils.ExecuteCommand(cmdArgs)
		require.NoError(o.T(), cmdOut.Err)
	}

	if o.useHelm {
		var err error
		k8sversion := o.kh.GetK8sServerVersion()
		logger.Infof("Installing rook on k8s %s", k8sversion)

		// disable the discovery daemonset with the helm chart
		settings := "enableDiscoveryDaemon=false"
		clusterNamespaces := "clusterNamespaces[0]=cluster-ns1,clusterNamespaces[1]=cluster-ns2"
		err = o.installer.CreateRookOperatorViaHelm(o.systemNamespace, settings, clusterNamespaces)
		require.NoError(o.T(), err)
	} else {
		err = o.installer.CreateCephOperator(installer.SystemNamespace(o.namespace1))
		require.NoError(o.T(), err)
	}

	require.True(o.T(), o.kh.IsPodInExpectedState("rook-ceph-operator", o.systemNamespace, "Running"),
		"Make sure rook-operator is in running state")

	if o.useHelm {
		_, err := o.kh.Clientset.AppsV1().DaemonSets(o.systemNamespace).Get("rook-discover", metav1.GetOptions{})
		require.True(o.T(), errors.IsNotFound(err))
	} else {
		require.True(o.T(), o.kh.IsPodInExpectedState("rook-discover", o.systemNamespace, "Running"),
			"Make sure rook-discover is in running state")
	}

	time.Sleep(10 * time.Second)

	// start the two clusters in parallel
	logger.Infof("starting two clusters, one traditional and one external")
	err = o.startCluster(o.namespace1, "bluestore")
	require.NoError(o.T(), err)
	// Wait for the monitors to be up so that we can fetch the configmap and secrets
	require.True(o.T(), o.kh.IsPodInExpectedState("rook-ceph-mon", o.namespace1, "Running"),
		"Make sure rook-ceph-mon is in running state")

	if o.externalCluster {
		// create an external cluster
		err = o.startExternalCluster(o.namespace2)
		require.NoError(o.T(), err)
	} else {
		err = o.startCluster(o.namespace2, "bluestore")
		require.NoError(o.T(), err)
		require.True(o.T(), o.kh.IsPodInExpectedState("rook-ceph-mon", o.namespace2, "Running"),
			"Make sure rook-ceph-mon is in running state")
	}

	logger.Infof("finished starting clusters")
}

// Teardown is a wrapper for tearDown after suite
func (o MCTestOperations) Teardown() {
	o.installer.UninstallRookFromMultipleNS(installer.SystemNamespace(o.namespace1), o.namespace1, o.namespace2)
}

func (o MCTestOperations) startCluster(namespace, store string) error {
	logger.Infof("starting cluster %s", namespace)
	err := o.installer.CreateRookCluster(namespace, o.systemNamespace, store, o.testOverPVC, o.storageClassName,
		cephv1.MonSpec{Count: 1, AllowMultiplePerNode: true}, true, false, installer.NautilusVersion)
	if err != nil {
		o.T().Fail()
		o.installer.GatherAllRookLogs(o.T().Name(), namespace, o.systemNamespace)
		return fmt.Errorf("failed to create cluster %s. %+v", namespace, err)
	}

	if err := o.installer.CreateRookToolbox(namespace); err != nil {
		o.T().Fail()
		o.installer.GatherAllRookLogs(o.T().Name(), namespace, o.systemNamespace)
		return fmt.Errorf("failed to create toolbox for %s. %+v", namespace, err)
	}
	logger.Infof("succeeded starting cluster %s", namespace)
	return nil
}

func (o MCTestOperations) startExternalCluster(namespace string) error {
	logger.Infof("starting external cluster %q", namespace)
	err := o.installer.CreateRookExternalCluster(namespace, o.namespace1)
	if err != nil {
		o.T().Fail()
		o.installer.GatherAllRookLogs(o.T().Name(), namespace, o.systemNamespace)
		return fmt.Errorf("failed to create external cluster %s. %+v", namespace, err)
	}

	logger.Infof("running toolbox on namespace %q", namespace)
	if err := o.installer.CreateRookToolbox(namespace); err != nil {
		o.T().Fail()
		o.installer.GatherAllRookLogs(o.T().Name(), namespace, o.systemNamespace)
		return fmt.Errorf("failed to create toolbox for %s. %+v", namespace, err)
	}

	logger.Infof("succeeded starting external cluster %s", namespace)
	return nil
}
