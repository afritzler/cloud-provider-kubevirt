/*
 * This file is part of the KubeVirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2017 Red Hat, Inc.
 *
 */

package tests_test

import (
	"flag"
	"strings"
	"time"

	expect "github.com/google/goexpect"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	k8sv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	v1 "kubevirt.io/kubevirt/pkg/api/v1"
	"kubevirt.io/kubevirt/pkg/kubecli"
	"kubevirt.io/kubevirt/tests"
)

var _ = Describe("ContainerDisk", func() {

	flag.Parse()

	virtClient, err := kubecli.GetKubevirtClient()
	tests.PanicOnError(err)

	BeforeEach(func() {
		tests.BeforeTestCleanup()
	})

	LaunchVMI := func(vmi *v1.VirtualMachineInstance) runtime.Object {
		By("Starting a VirtualMachineInstance")
		obj, err := virtClient.RestClient().Post().Resource("virtualmachineinstances").Namespace(tests.NamespaceTestDefault).Body(vmi).Do().Get()
		Expect(err).To(BeNil())
		return obj
	}

	VerifyContainerDiskVMI := func(vmi *v1.VirtualMachineInstance, obj runtime.Object, ignoreWarnings bool) {
		_, ok := obj.(*v1.VirtualMachineInstance)
		Expect(ok).To(BeTrue(), "Object is not of type *v1.VirtualMachineInstance")
		if ignoreWarnings == true {
			tests.WaitForSuccessfulVMIStartIgnoreWarnings(obj)
		} else {
			tests.WaitForSuccessfulVMIStart(obj)
		}

		// Verify Registry Disks are Online
		pods, err := virtClient.CoreV1().Pods(tests.NamespaceTestDefault).List(tests.UnfinishedVMIPodSelector(vmi))
		Expect(err).To(BeNil())

		By("Checking the number of VirtualMachineInstance disks")
		disksFound := 0
		for _, pod := range pods.Items {
			if pod.ObjectMeta.DeletionTimestamp != nil {
				continue
			}
			for _, containerStatus := range pod.Status.ContainerStatuses {
				if strings.HasPrefix(containerStatus.Name, "volume") == false {
					// only check readiness of disk containers
					continue
				}
				disksFound++
			}
			break
		}
		Expect(disksFound).To(Equal(1))
	}

	Describe("Starting and stopping the same VirtualMachineInstance", func() {
		Context("with ephemeral registry disk", func() {
			It("should success multiple times", func() {
				vmi := tests.NewRandomVMIWithEphemeralDiskAndUserdata(tests.ContainerDiskFor(tests.ContainerDiskCirros), "#!/bin/bash\necho 'hello'\n")
				num := 2
				for i := 0; i < num; i++ {
					By("Starting the VirtualMachineInstance")
					obj, err := virtClient.RestClient().Post().Resource("virtualmachineinstances").Namespace(tests.NamespaceTestDefault).Body(vmi).Do().Get()
					Expect(err).To(BeNil())
					tests.WaitForSuccessfulVMIStart(obj)

					By("Stopping the VirtualMachineInstance")
					_, err = virtClient.RestClient().Delete().Resource("virtualmachineinstances").Namespace(vmi.GetObjectMeta().GetNamespace()).Name(vmi.GetObjectMeta().GetName()).Do().Get()
					Expect(err).To(BeNil())
					By("Waiting until the VirtualMachineInstance is gone")
					tests.WaitForVirtualMachineToDisappearWithTimeout(vmi, 120)
				}
			})
		})
	})

	Describe("Starting a VirtualMachineInstance", func() {
		Context("with ephemeral registry disk", func() {
			It("should not modify the spec on status update", func() {
				vmi := tests.NewRandomVMIWithEphemeralDiskAndUserdata(tests.ContainerDiskFor(tests.ContainerDiskCirros), "#!/bin/bash\necho 'hello'\n")
				v1.SetObjectDefaults_VirtualMachineInstance(vmi)

				By("Starting the VirtualMachineInstance")
				vmi, err := virtClient.VirtualMachineInstance(tests.NamespaceTestDefault).Create(vmi)
				Expect(err).To(BeNil())
				tests.WaitForSuccessfulVMIStart(vmi)
				startedVMI, err := virtClient.VirtualMachineInstance(tests.NamespaceTestDefault).Get(vmi.ObjectMeta.Name, &metav1.GetOptions{})
				Expect(err).To(BeNil())
				By("Checking that the VirtualMachineInstance spec did not change")
				Expect(startedVMI.Spec).To(Equal(vmi.Spec))
			})
		})
	})

	Describe("Starting multiple VMIs", func() {
		Context("with ephemeral registry disk", func() {
			It("should success", func() {
				num := 5
				vmis := make([]*v1.VirtualMachineInstance, 0, num)
				objs := make([]runtime.Object, 0, num)
				for i := 0; i < num; i++ {
					vmi := tests.NewRandomVMIWithEphemeralDiskAndUserdata(tests.ContainerDiskFor(tests.ContainerDiskCirros), "#!/bin/bash\necho 'hello'\n")
					// FIXME if we give too much ram, the vmis really boot and eat all our memory (cache?)
					vmi.Spec.Domain.Resources.Requests[k8sv1.ResourceMemory] = resource.MustParse("1M")
					obj := LaunchVMI(vmi)
					vmis = append(vmis, vmi)
					objs = append(objs, obj)
				}

				for idx, vmi := range vmis {
					// TODO once networking is implemented properly set ignoreWarnings == false here.
					// We have to ignore warnings because VMIs started in parallel
					// may cause libvirt to fail to create the macvtap device in
					// the host network.
					// The new network implementation we're working on should resolve this.
					// NOTE the VirtualMachineInstance still starts successfully regardless of this warning.
					// It just requires virt-handler to retry the Start command at the moment.
					VerifyContainerDiskVMI(vmi, objs[idx], true)
				}
			}) // Timeout is long because this test involves multiple parallel VirtualMachineInstance launches.
		})
	})

	Describe("Starting from custom image location", func() {
		Context("with disk at /custom-disk/boot.img", func() {
			It("should boot normally", func() {
				vmi := tests.NewRandomVMIWithEphemeralDiskAndUserdata(tests.ContainerDiskFor(tests.ContainerDiskCirros), "#!/bin/bash\necho 'hello'\n")
				for ind, volume := range vmi.Spec.Volumes {
					if volume.ContainerDisk != nil {
						vmi.Spec.Volumes[ind].ContainerDisk.Path = "/custom-disk/boot.img"
					}
				}
				By("Starting the VirtualMachineInstance")
				obj, err := virtClient.RestClient().Post().Resource("virtualmachineinstances").Namespace(tests.NamespaceTestDefault).Body(vmi).Do().Get()
				Expect(err).To(BeNil())
				tests.WaitForSuccessfulVMIStart(obj)
			})
		})
	})

	Describe("Starting with virtio-win", func() {
		Context("with virtio-win as secondary disk", func() {
			It("should boot and have the virtio as sata CDROM", func() {
				vmi := tests.NewRandomVMIWithEphemeralDisk(tests.ContainerDiskFor(tests.ContainerDiskAlpine))
				tests.AddEphemeralCdrom(vmi, "disk4", "sata", tests.ContainerDiskFor(tests.ContainerDiskVirtio))

				By("Starting the VirtualMachineInstance")
				obj, err := virtClient.RestClient().Post().Resource("virtualmachineinstances").Namespace(tests.NamespaceTestDefault).Body(vmi).Do().Get()
				Expect(err).To(BeNil(), "expected vmi to start with no problem")
				tests.WaitForSuccessfulVMIStart(obj)

				By("Checking whether the second disk really contains virtio drivers")
				expecter, err := tests.LoggedInAlpineExpecter(vmi)
				Expect(err).ToNot(HaveOccurred(), "expected alpine to login properly")
				defer expecter.Close()

				_, err = expecter.ExpectBatch([]expect.Batcher{
					// mount virtio cdrom and check files are there
					&expect.BSnd{S: "mount -t iso9600 /dev/cdrom\n"},
					&expect.BSnd{S: "echo $?\n"},
					&expect.BExp{R: "0"},
					&expect.BSnd{S: "cd /media/cdrom\n"},
					&expect.BSnd{S: "ls virtio-win_license.txt guest-agent\n"},
					&expect.BSnd{S: "echo $?\n"},
					&expect.BExp{R: "0"},
				}, 200*time.Second)
				Expect(err).ToNot(HaveOccurred(), "expected virtio files to be mounted properly")
			})
		})
	})
})
