/*
Copyright 2020 The Knative Authors

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

package resources

import (
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeinformers "k8s.io/client-go/informers"
	fakek8s "k8s.io/client-go/kubernetes/fake"
	"knative.dev/serving/pkg/apis/serving"
)

const testRevision = "test-revision"

func TestPodsSortedByAge(t *testing.T) {
	aTime := time.Now()

	tests := []struct {
		name string
		pods []*corev1.Pod
		want []string
	}{{
		name: "no pods",
	}, {
		name: "one pod",
		pods: []*corev1.Pod{
			pod("master-of-puppets", withStartTime(aTime), withIP("1.1.1.1")),
		},
		want: []string{"1.1.1.1"},
	}, {
		name: "more than 1 pod, sorted",
		pods: []*corev1.Pod{
			pod("ride-the-lightning", withStartTime(aTime), withIP("1.9.8.2")),
			pod("fade-to-black", withStartTime(aTime.Add(time.Second)), withIP("1.9.8.4")),
			pod("battery", withStartTime(time.Now().Add(time.Minute)), withIP("1.9.8.8")),
		},
		want: []string{"1.9.8.2", "1.9.8.4", "1.9.8.8"},
	}, {
		name: "more than 1 pod, unsorted",
		pods: []*corev1.Pod{
			pod("one", withStartTime(aTime), withIP("2.0.0.6")),
			pod("seek-and-destroy", withStartTime(aTime.Add(-time.Second)), withIP("2.0.0.3")),
			pod("metal-militia", withStartTime(time.Now().Add(time.Minute)), withIP("2.0.0.9")),
		},
		want: []string{"2.0.0.3", "2.0.0.6", "2.0.0.9"},
	}, {
		name: "more than 1 pod, unsorted, preserve order",
		pods: []*corev1.Pod{
			pod("nothing-else-matters", withStartTime(aTime), withIP("1.2.3.4")),
			pod("wherever-i-may-roam", withStartTime(aTime.Add(-time.Second)), withIP("2.3.4.5")),
			pod("sad-but-true", withStartTime(time.Now().Add(time.Minute)), withIP("3.4.5.6")),
			pod("enter-sandman", withStartTime(time.Now()), withIP("1.2.3.5")),
		},
		want: []string{"2.3.4.5", "1.2.3.4", "1.2.3.5", "3.4.5.6"},
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {

			kubeClient := fakek8s.NewSimpleClientset()
			podsClient := kubeinformers.NewSharedInformerFactory(kubeClient, 0).Core().V1().Pods()
			for _, p := range tc.pods {
				kubeClient.CoreV1().Pods(testNamespace).Create(p)
				podsClient.Informer().GetIndexer().Add(p)
			}
			podCounter := NewPodAccessor(podsClient.Lister(), testNamespace, testRevision)

			got, err := podCounter.PodIPsByAge()
			if err != nil {
				t.Fatal("PodIPsByAge failed:", err)
			}
			if want := tc.want; !cmp.Equal(got, want, cmpopts.EquateEmpty()) {
				t.Error("PodIPsByAge wrong answer (-want, +got):\n", cmp.Diff(want, got, cmpopts.EquateEmpty()))
			}
		})
	}
}

func TestScopedPodsCounter(t *testing.T) {
	kubeClient := fakek8s.NewSimpleClientset()
	podsClient := kubeinformers.NewSharedInformerFactory(kubeClient, 0).Core().V1().Pods()
	createPods := func(pods []*corev1.Pod) {
		for _, p := range pods {
			kubeClient.CoreV1().Pods(testNamespace).Create(p)
			podsClient.Informer().GetIndexer().Add(p)
		}
	}

	podCounter := NewPodAccessor(podsClient.Lister(), testNamespace, testRevision)

	tests := []struct {
		name            string
		pods            []*corev1.Pod
		wantRunning     int
		wantPending     int
		wantTerminating int
		wantErr         bool
	}{{
		name:            "no pods",
		pods:            podsInPhases(0, 0, 0),
		wantRunning:     0,
		wantPending:     0,
		wantTerminating: 0,
	}, {
		name:            "one running/two pending/three terminating pod",
		pods:            podsInPhases(1, 2, 3),
		wantRunning:     1,
		wantPending:     2,
		wantTerminating: 3,
	}, {
		name:            "ten running/eleven pending/twelve terminating pods",
		pods:            podsInPhases(10, 11, 12),
		wantRunning:     10,
		wantPending:     11,
		wantTerminating: 12,
	}}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			createPods(test.pods)

			pending, terminating, err := podCounter.PendingTerminatingCount()
			if got, want := (err != nil), test.wantErr; got != want {
				t.Errorf("WantErr = %v, want: %v, err: %v", got, want, err)
			}

			if pending != test.wantPending {
				t.Errorf("PendingCount() = %d, want: %d", pending, test.wantPending)
			}

			if terminating != test.wantTerminating {
				t.Errorf("TerminatingCount() = %d, want: %d", terminating, test.wantTerminating)
			}
		})
	}
}

type podOption func(p *corev1.Pod)

func pod(name string, pos ...podOption) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels:    map[string]string{serving.RevisionLabelKey: testRevision},
		},
		Status: corev1.PodStatus{},
	}
	for _, po := range pos {
		po(p)
	}
	return p
}

func withPhase(ph corev1.PodPhase) podOption {
	return func(p *corev1.Pod) {
		p.Status.Phase = ph
	}
}

func withStartTime(t time.Time) podOption {
	tm := metav1.NewTime(t)
	return func(p *corev1.Pod) {
		p.Status.StartTime = &tm
	}
}

func withIP(ip string) podOption {
	return func(p *corev1.Pod) {
		p.Status.PodIP = ip
	}
}

// Shortcut for a much used combo.
func phasedPod(name string, phase corev1.PodPhase) *corev1.Pod {
	return pod(name, withPhase(phase))
}

func podsInPhases(running, pending, terminating int) []*corev1.Pod {
	pods := make([]*corev1.Pod, 0, running+pending+terminating)

	for i := 0; i < running; i++ {
		pods = append(pods, phasedPod("running-pod-"+strconv.Itoa(i), corev1.PodRunning))
	}

	now := metav1.Now()
	for i := 0; i < terminating; i++ {
		p := phasedPod("terminating-pod-"+strconv.Itoa(i), corev1.PodRunning)
		p.DeletionTimestamp = &now
		pods = append(pods, p)
	}

	for i := 0; i < pending; i++ {
		pods = append(pods, phasedPod("pending-pod-"+strconv.Itoa(i), corev1.PodPending))
	}
	return pods
}
