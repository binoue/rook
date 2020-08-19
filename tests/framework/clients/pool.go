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

package clients

import (
	"fmt"
	"strconv"
	"time"

	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	"github.com/rook/rook/pkg/daemon/ceph/client"
	"github.com/rook/rook/tests/framework/installer"
	"github.com/rook/rook/tests/framework/utils"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PoolOperation is a wrapper for rook pool operations
type PoolOperation struct {
	k8sh      *utils.K8sHelper
	manifests installer.CephManifests
}

// CreatePoolOperation creates a new pool client
func CreatePoolOperation(k8sh *utils.K8sHelper, manifests installer.CephManifests) *PoolOperation {
	return &PoolOperation{k8sh, manifests}
}

func (p *PoolOperation) Create(name, namespace string, replicas int) error {
	return p.createOrUpdatePool(name, namespace, "apply", replicas)
}

func (p *PoolOperation) Update(name, namespace string, replicas int) error {
	return p.createOrUpdatePool(name, namespace, "apply", replicas)
}

func (p *PoolOperation) createOrUpdatePool(name, namespace, action string, replicas int) error {
	return p.k8sh.ResourceOperation(action, p.manifests.GetBlockPoolDef(name, namespace, strconv.Itoa(replicas)))
}

func (p *PoolOperation) ListCephPools(clusterInfo *client.ClusterInfo) ([]client.CephStoragePoolSummary, error) {
	context := p.k8sh.MakeContext()
	pools, err := client.ListPoolSummaries(context, clusterInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to list pools: %+v", err)
	}
	return pools, nil
}

func (p *PoolOperation) GetCephPoolDetails(clusterInfo *client.ClusterInfo, name string) (client.CephStoragePoolDetails, error) {
	context := p.k8sh.MakeContext()
	details, err := client.GetPoolDetails(context, clusterInfo, name)
	if err != nil {
		return client.CephStoragePoolDetails{}, fmt.Errorf("failed to get pool %s details: %+v", name, err)
	}
	return details, nil
}

func (p *PoolOperation) ListPoolCRDs(namespace string) ([]cephv1.CephBlockPool, error) {
	pools, err := p.k8sh.RookClientset.CephV1().CephBlockPools(namespace).List(metav1.ListOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	return pools.Items, nil
}

func (p *PoolOperation) PoolCRDExists(namespace, name string) (bool, error) {
	_, err := p.k8sh.RookClientset.CephV1().CephBlockPools(namespace).Get(name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (p *PoolOperation) CephPoolExists(namespace, name string) (bool, error) {
	clusterInfo := client.AdminClusterInfo(namespace)
	pools, err := p.ListCephPools(clusterInfo)
	if err != nil {
		return false, err
	}
	for _, pool := range pools {
		if pool.Name == name {
			return true, nil
		}
	}
	return false, nil
}

// DeletePool deletes a pool after deleting all the block images contained by the pool
func (p *PoolOperation) DeletePool(blockClient *BlockOperation, clusterInfo *client.ClusterInfo, poolName string) error {
	// Delete all the images in a pool
	logger.Infof("listing images in pool %q", poolName)
	blockImagesList, err := blockClient.ListImagesInPool(clusterInfo, poolName)
	if err != nil {
		logger.Infof("failed to retrieve block images. err: %v", err)
	}
	for _, blockImage := range blockImagesList {
		logger.Infof("force deleting block image %q in pool %q", blockImage, poolName)
		// Wait and retry up to 10 times/seconds to delete RBD images
		for i := 0; i < 10; i++ {
			err := blockClient.DeleteBlockImage(clusterInfo, blockImage)
			if err != nil {
				logger.Infof("failed deleting image %q from %q. %v", blockImage, poolName, err)
				time.Sleep(2 * time.Second)
			} else {
				break
			}
			return fmt.Errorf("gave up waiting for image %q from %q to be deleted. %v", blockImage, poolName, err)
		}
	}

	logger.Infof("deleting pool CR %q", poolName)
	err = p.k8sh.RookClientset.CephV1().CephBlockPools(clusterInfo.Namespace).Delete(poolName, &metav1.DeleteOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete pool CR. %v", err)
	}

	crdCheckerFunc := func() error {
		_, err := p.k8sh.RookClientset.CephV1().CephBlockPools(clusterInfo.Namespace).Get(poolName, metav1.GetOptions{})
		return err
	}

	return p.k8sh.WaitForCustomResourceDeletion(clusterInfo.Namespace, crdCheckerFunc)
}
