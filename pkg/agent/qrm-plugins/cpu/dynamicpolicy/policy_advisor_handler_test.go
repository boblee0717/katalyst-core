/*
Copyright 2022 The Katalyst Authors.

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

package dynamicpolicy

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kubewharf/katalyst-core/pkg/agent/qrm-plugins/advisorsvc"
	advisorapi "github.com/kubewharf/katalyst-core/pkg/agent/qrm-plugins/cpu/dynamicpolicy/cpuadvisor"

	"github.com/kubewharf/katalyst-core/pkg/util/native"

	"github.com/kubewharf/katalyst-core/pkg/util/cgroup/common"
	cgroupmgr "github.com/kubewharf/katalyst-core/pkg/util/cgroup/manager"

	"github.com/bytedance/mockey"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	resource2 "k8s.io/apimachinery/pkg/api/resource"

	"github.com/kubewharf/katalyst-core/pkg/metaserver"
	"github.com/kubewharf/katalyst-core/pkg/metaserver/agent"
	"github.com/kubewharf/katalyst-core/pkg/metaserver/agent/pod"
)

var advisorTestMutex = &sync.Mutex{}

func TestDynamicPolicy_checkAndApplyIfCgroupV1(t *testing.T) {
	t.Parallel()

	mockPod := &v1.Pod{
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name: "test-container",
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU: resource2.MustParse("2"),
						},
					},
				},
			},
		},
	}

	mockPodPathMap := map[string]*v1.Pod{
		"test-pod-1": mockPod,
	}

	resources := &common.CgroupResources{
		CpuQuota:  1000,
		CpuPeriod: 1000,
	}

	mockBytes, _ := json.Marshal(resources)

	mockCal := &advisorsvc.CalculationInfo{
		CgroupPath: "test_cgroup_path",
		CalculationResult: &advisorsvc.CalculationResult{
			Values: map[string]string{
				string(advisorapi.ControlKnobKeyCgroupConfig): string(mockBytes),
			},
		},
	}

	p := &DynamicPolicy{
		metaServer: &metaserver.MetaServer{
			MetaAgent: &agent.MetaAgent{
				PodFetcher: &pod.PodFetcherStub{},
			},
		},
	}

	advisorTestMutex.Lock()
	defer advisorTestMutex.Unlock()

	mockey.PatchConvey("test cgroup v1 resource", t, func() {
		mockey.Mock(common.CheckCgroup2UnifiedMode).IncludeCurrentGoRoutine().Return(false).Build()
		mockey.Mock((*DynamicPolicy).getCurrentPathAllPodsDirAndMap).IncludeCurrentGoRoutine().Return(mockPodPathMap, []string{"advisor-test-pod-1"}, nil).Build()
		mockey.Mock((*DynamicPolicy).getPodAndRelativePath).IncludeCurrentGoRoutine().Return(mockPod, "test_relative_path", nil).Build()
		mockey.Mock((*DynamicPolicy).checkAllContainersQuota).IncludeCurrentGoRoutine().Return(nil).Build()
		mockey.Mock((*DynamicPolicy).applyCPUQuotaWithRelativePath).IncludeCurrentGoRoutine().Return(nil).Build()

		err := p.checkAndApplyIfCgroupV1(mockCal, resources)

		convey.So(err, convey.ShouldBeNil)
	})
}

func TestDynamicPolicy_getAllDirs(t *testing.T) {
	t.Parallel()

	policy := &DynamicPolicy{}

	t.Run("basic path", func(t *testing.T) {
		t.Parallel()

		advisorTestMutex.Lock()
		defer advisorTestMutex.Unlock()
		_ = mockey.Mock(os.ReadDir).IncludeCurrentGoRoutine().To(func(dirname string) ([]os.DirEntry, error) {
			return []os.DirEntry{
				mockDirEntry{"foo", true},
				mockDirEntry{"bar", true},
			}, nil
		}).Build()

		dirs, err := policy.getAllDirs("/fake/path")
		assert.NoError(t, err)
		assert.ElementsMatch(t, dirs, []string{"foo", "bar"})
	})
}

type mockDirEntry struct {
	name  string
	isDir bool
}

func (m mockDirEntry) Name() string               { return m.name }
func (m mockDirEntry) IsDir() bool                { return m.isDir }
func (m mockDirEntry) Type() os.FileMode          { return 0 }
func (m mockDirEntry) Info() (os.FileInfo, error) { return nil, nil }

func TestDynamic_getCurrentPathAllPodsDirAndMap(t *testing.T) {
	t.Parallel()

	advisorTestMutex.Lock()
	defer advisorTestMutex.Unlock()

	mockey.PatchConvey("test getCurrentPathAllPodsDirAndMap", t, func() {
		mockPodPathMap := map[string]*v1.Pod{
			"test-pod-1": {
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name: "test-container",
							Resources: v1.ResourceRequirements{
								Requests: v1.ResourceList{
									v1.ResourceCPU: resource2.MustParse("2"),
								},
							},
						},
					},
				},
			},
		}
		mockey.Mock((*DynamicPolicy).getAllPodsPathMap).IncludeCurrentGoRoutine().Return(mockPodPathMap, nil).Build()
		mockey.Mock((*DynamicPolicy).getAllDirs).IncludeCurrentGoRoutine().Return([]string{"advisor-test-pod-1"}, nil).Build()

		p := &DynamicPolicy{
			metaServer: &metaserver.MetaServer{
				MetaAgent: &agent.MetaAgent{
					PodFetcher: &pod.PodFetcherStub{},
				},
			},
		}

		resultMap, dirs, err := p.getCurrentPathAllPodsDirAndMap("test_group_path")
		convey.So(err, convey.ShouldBeNil)
		convey.So(resultMap, convey.ShouldNotBeNil)
		convey.So(dirs, convey.ShouldNotBeNil)
	})
}

func TestDynamicPolicy_getPodAndRelativePath(t *testing.T) {
	t.Parallel()

	advisorTestMutex.Lock()
	defer advisorTestMutex.Unlock()

	currentPath := "test"
	dirs := "test-dir"
	podPathMap := map[string]*v1.Pod{
		common.GetAbsCgroupPath(common.DefaultSelectedSubsys, filepath.Join(currentPath, dirs)): {
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name: "test-container",
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU: resource2.MustParse("2"),
							},
						},
					},
				},
			},
		},
	}

	p := &DynamicPolicy{
		metaServer: &metaserver.MetaServer{
			MetaAgent: &agent.MetaAgent{
				PodFetcher: &pod.PodFetcherStub{},
			},
		},
	}

	mockey.PatchConvey("test getPodAndRelativePath", t, func() {
		_, _, err := p.getPodAndRelativePath(currentPath, dirs, podPathMap)
		convey.So(err, convey.ShouldBeNil)
	})
}

func TestDynamicPolicy_getAllPodsPathMap(t *testing.T) {
	t.Parallel()

	mockPods := []*v1.Pod{
		{
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{
						Name: "test-container",
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU: resource2.MustParse("2"),
							},
						},
					},
				},
			},
		},
	}

	p := &DynamicPolicy{
		metaServer: &metaserver.MetaServer{
			MetaAgent: &agent.MetaAgent{
				PodFetcher: &pod.PodFetcherStub{},
			},
		},
	}

	advisorTestMutex.Lock()
	defer advisorTestMutex.Unlock()
	mockey.PatchConvey("test getAllPodsPathMap", t, func() {
		mockey.Mock((*pod.PodFetcherStub).GetPodList).IncludeCurrentGoRoutine().Return(mockPods, nil).Build()
		mockey.Mock(common.GetPodAbsCgroupPath).IncludeCurrentGoRoutine().Return("test-pod-1-path", nil).Build()

		podPathMap, err := p.getAllPodsPathMap()

		convey.So(err, convey.ShouldBeNil)
		convey.So(len(podPathMap), convey.ShouldEqual, len(mockPods))
		convey.So(podPathMap["test-pod-1-path"], convey.ShouldEqual, mockPods[0])
	})
}

func TestDynamicPolicy_getAllContainersRelativePathMap(t *testing.T) {
	t.Parallel()

	mockPod := &v1.Pod{
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name: "test-container",
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU: resource2.MustParse("2"),
						},
					},
				},
			},
		},
	}

	p := &DynamicPolicy{
		metaServer: &metaserver.MetaServer{
			MetaAgent: &agent.MetaAgent{
				PodFetcher: &pod.PodFetcherStub{},
			},
		},
	}

	advisorTestMutex.Lock()
	defer advisorTestMutex.Unlock()
	mockey.PatchConvey("test getAllContainersRelativePathMap", t, func() {
		mockey.Mock(native.GetContainerID).IncludeCurrentGoRoutine().Return("test-container-ID", nil).Build()
		mockey.Mock(common.GetContainerRelativeCgroupPath).IncludeCurrentGoRoutine().Return("test-container-relative-path", nil).Build()

		testMap := p.getAllContainersRelativePathMap(mockPod)

		convey.So(len(testMap), convey.ShouldEqual, 1)
		convey.So(testMap["test-container-relative-path"].Name, convey.ShouldEqual, mockPod.Spec.Containers[0].Name)
	})
}

func TestDynamicPolicy_applyCPUQuotaWithRelativePath(t *testing.T) {
	t.Parallel()

	res := &common.CgroupResources{
		CpuQuota: 500000,
	}

	mockCPU1 := &common.CPUStats{
		CpuQuota:  -1,
		CpuPeriod: 1000,
	}

	mockCPU2 := &common.CPUStats{
		CpuQuota:  1000,
		CpuPeriod: 1000,
	}

	p := &DynamicPolicy{
		metaServer: &metaserver.MetaServer{
			MetaAgent: &agent.MetaAgent{
				PodFetcher: &pod.PodFetcherStub{},
			},
		},
	}

	advisorTestMutex.Lock()
	defer advisorTestMutex.Unlock()
	mockey.PatchConvey("test applyCPUQuotaWithRelativePath", t, func() {
		mockey.Mock(cgroupmgr.GetCPUWithRelativePath).IncludeCurrentGoRoutine().To(func(path string) (*common.CPUStats, error) {
			if path == "test-relative-path-1" {
				return mockCPU1, nil
			}
			if path == "test-relative-path-2" {
				return mockCPU2, nil
			}

			return nil, errors.New("not found")
		}).Build()

		apply := mockey.Mock(cgroupmgr.ApplyCPUWithRelativePath).IncludeCurrentGoRoutine().Return(nil).Build()

		err := p.applyCPUQuotaWithRelativePath("test-relative-path-1", 400, res)
		convey.So(err, convey.ShouldBeNil)
		convey.So(apply.Times(), convey.ShouldEqual, 1)

		err = p.applyCPUQuotaWithRelativePath("test-relative-path-2", 1000, res)
		convey.So(err, convey.ShouldBeNil)
		convey.So(apply.Times(), convey.ShouldEqual, 2)
	})
}

func TestDynamicPolicy_checkAllContainersQuota(t *testing.T) {
	t.Parallel()

	p := &DynamicPolicy{
		metaServer: &metaserver.MetaServer{
			MetaAgent: &agent.MetaAgent{
				PodFetcher: &pod.PodFetcherStub{},
			},
		},
	}

	containerPathMap := map[string]*v1.Container{
		"container1": {
			Name: "container1",
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceCPU: resource2.MustParse("1"),
				},
			},
		},
		"container2": {
			Name: "container2",
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceCPU: resource2.MustParse("2"),
				},
			},
		},
	}

	pod := &v1.Pod{
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name: "container1",
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU: resource2.MustParse("1"),
						},
					},
				},
				{
					Name: "container2",
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceCPU: resource2.MustParse("2"),
						},
					},
				},
			},
		},
	}

	advisorTestMutex.Lock()
	defer advisorTestMutex.Unlock()
	mockey.PatchConvey("test checkAllContainersQuota", t, func() {
		mockey.Mock((*DynamicPolicy).getAllContainersRelativePathMap).IncludeCurrentGoRoutine().Return(containerPathMap).Build()
		apply := mockey.Mock((*DynamicPolicy).applyCPUQuotaWithRelativePath).IncludeCurrentGoRoutine().Return(nil).Build()

		err := p.checkAllContainersQuota(pod, nil)

		convey.So(err, convey.ShouldBeNil)
		convey.So(apply.Times(), convey.ShouldEqual, 2)
	})
}
