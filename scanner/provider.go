package scanner

import (
	"fmt"
	"github.com/MagalixCorp/magalix-agent/v2/entities"
	"github.com/MagalixCorp/magalix-agent/v2/kuber"
	"github.com/MagalixCorp/magalix-agent/v2/proto"
	"github.com/MagalixCorp/magalix-agent/v2/utils"
	"github.com/reconquest/karma-go"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/labels"
	"regexp"
	"sync"

	appsv1beta2 "k8s.io/api/apps/v1beta2"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
)

// this file contains an adapter to adapt resources from entities.EntitiesWatcher
// to resources known by the scanner.
// TODO: remove this adapter after deprecating use of scanner

type kubeObserver struct {
	watcher entities.EntitiesWatcher
}

func NewKuberFromObserver(watcher entities.EntitiesWatcher) *kubeObserver {
	return &kubeObserver{
		watcher: watcher,
	}
}

func (kube *kubeObserver) get(
	gvrk kuber.GroupVersionResourceKind,
	result interface{},
) error {
	w, err := kube.watcher.WatcherFor(gvrk)
	if err != nil {
		return err
	}
	ret, err := w.Lister().List(labels.Everything())
	if err != nil {
		return karma.Format(
			err,
			"unable to retrieve nodes",
		)
	}

	err = utils.Transcode(ret, result)
	if err != nil {
		return err
	}

	return nil
}

func (kube *kubeObserver) GetNodes() (*corev1.NodeList, error) {
	var items []corev1.Node
	err := kube.get(kuber.Nodes, &items)
	if err != nil {
		return nil, err
	}
	return &corev1.NodeList{
		Items: items,
	}, nil
}

func (kube *kubeObserver) GetPods() (*corev1.PodList, error) {
	var items []corev1.Pod
	err := kube.get(kuber.Pods, &items)
	if err != nil {
		return nil, err
	}
	return &corev1.PodList{
		Items: items,
	}, nil
}

func (kube *kubeObserver) GetResources() (
	pods []corev1.Pod,
	limitRanges []corev1.LimitRange,
	resources []kuber.Resource,
	rawResources map[string]interface{},
	err error,
) {
	rawResources = map[string]interface{}{}

	m := sync.Mutex{}
	group := errgroup.Group{}

	group.Go(func() error {
		controllers, err := kube.GetReplicationControllers()
		if err != nil {
			return karma.Format(
				err,
				"unable to get replication controllers",
			)
		}

		if controllers != nil {
			m.Lock()
			defer m.Unlock()

			rawResources["controllers"] = controllers

			for _, controller := range controllers.Items {
				resources = append(resources, kuber.Resource{
					Kind:        "ReplicationController",
					Annotations: controller.Annotations,
					Namespace:   controller.Namespace,
					Name:        controller.Name,
					Containers:  controller.Spec.Template.Spec.Containers,
					PodRegexp: regexp.MustCompile(
						fmt.Sprintf(
							"^%s-[^-]+$",
							regexp.QuoteMeta(controller.Name),
						),
					),
					ReplicasStatus: proto.ReplicasStatus{
						Desired:   controller.Spec.Replicas,
						Current:   newInt32Pointer(controller.Status.Replicas),
						Ready:     newInt32Pointer(controller.Status.ReadyReplicas),
						Available: newInt32Pointer(controller.Status.AvailableReplicas),
					},
				})
			}
		}
		return nil
	})

	group.Go(func() error {
		podList, err := kube.GetPods()
		if err != nil {
			return karma.Format(
				err,
				"unable to get pods",
			)
		}

		if podList != nil {
			pods = podList.Items

			m.Lock()
			defer m.Unlock()

			rawResources["pods"] = podList

			for _, pod := range pods {
				if len(pod.OwnerReferences) > 0 {
					continue
				}
				resources = append(resources, kuber.Resource{
					Kind:        "OrphanPod",
					Annotations: pod.Annotations,
					Namespace:   pod.Namespace,
					Name:        pod.Name,
					Containers:  pod.Spec.Containers,
					PodRegexp: regexp.MustCompile(
						fmt.Sprintf(
							"^%s$",
							regexp.QuoteMeta(pod.Name),
						),
					),
					ReplicasStatus: proto.ReplicasStatus{
						Desired:   newInt32Pointer(1),
						Current:   newInt32Pointer(1),
						Ready:     newInt32Pointer(1),
						Available: newInt32Pointer(1),
					},
				})
			}
		}

		return nil
	})

	group.Go(func() error {
		deployments, err := kube.GetDeployments()
		if err != nil {
			return karma.Format(
				err,
				"unable to get deployments",
			)
		}

		if deployments != nil {
			m.Lock()
			defer m.Unlock()

			rawResources["deployments"] = deployments

			for _, deployment := range deployments.Items {
				resources = append(resources, kuber.Resource{
					Kind:        "Deployment",
					Annotations: deployment.Annotations,
					Namespace:   deployment.Namespace,
					Name:        deployment.Name,
					Containers:  deployment.Spec.Template.Spec.Containers,
					PodRegexp: regexp.MustCompile(
						fmt.Sprintf(
							"^%s-[^-]+-[^-]+$",
							regexp.QuoteMeta(deployment.Name),
						),
					),
					ReplicasStatus: proto.ReplicasStatus{
						Desired:   deployment.Spec.Replicas,
						Current:   newInt32Pointer(deployment.Status.Replicas),
						Ready:     newInt32Pointer(deployment.Status.ReadyReplicas),
						Available: newInt32Pointer(deployment.Status.AvailableReplicas),
					},
				})
			}
		}

		return nil
	})

	group.Go(func() error {
		statefulSets, err := kube.GetStatefulSets()
		if err != nil {
			return karma.Format(
				err,
				"unable to get statefulSets",
			)
		}

		if statefulSets != nil {
			m.Lock()
			defer m.Unlock()

			rawResources["statefulSets"] = statefulSets

			for _, set := range statefulSets.Items {
				resources = append(resources, kuber.Resource{
					Kind:        "StatefulSet",
					Annotations: set.Annotations,
					Namespace:   set.Namespace,
					Name:        set.Name,
					Containers:  set.Spec.Template.Spec.Containers,
					PodRegexp: regexp.MustCompile(
						fmt.Sprintf(
							"^%s-([0-9]+)$",
							regexp.QuoteMeta(set.Name),
						),
					),
					ReplicasStatus: proto.ReplicasStatus{
						Desired:   set.Spec.Replicas,
						Current:   newInt32Pointer(set.Status.Replicas),
						Ready:     newInt32Pointer(set.Status.ReadyReplicas),
						Available: newInt32Pointer(set.Status.CurrentReplicas),
					},
				})
			}
		}

		return nil
	})

	group.Go(func() error {
		daemonSets, err := kube.GetDaemonSets()
		if err != nil {
			return karma.Format(
				err,
				"unable to get daemonSets",
			)
		}

		if daemonSets != nil {
			m.Lock()
			defer m.Unlock()

			rawResources["daemonSets"] = daemonSets

			for _, daemon := range daemonSets.Items {
				resources = append(resources, kuber.Resource{
					Kind:        "DaemonSet",
					Annotations: daemon.Annotations,
					Namespace:   daemon.Namespace,
					Name:        daemon.Name,
					Containers:  daemon.Spec.Template.Spec.Containers,
					PodRegexp: regexp.MustCompile(
						fmt.Sprintf(
							"^%s-[^-]+$",
							regexp.QuoteMeta(daemon.Name),
						),
					),
					ReplicasStatus: proto.ReplicasStatus{
						Desired:   newInt32Pointer(daemon.Status.DesiredNumberScheduled),
						Current:   newInt32Pointer(daemon.Status.CurrentNumberScheduled),
						Ready:     newInt32Pointer(daemon.Status.NumberReady),
						Available: newInt32Pointer(daemon.Status.NumberAvailable),
					},
				})
			}
		}

		return nil
	})

	group.Go(func() error {
		replicaSets, err := kube.GetReplicaSets()
		if err != nil {
			return karma.Format(
				err,
				"unable to get replicasets",
			)
		}

		if replicaSets != nil {
			m.Lock()
			defer m.Unlock()

			rawResources["replicaSets"] = replicaSets

			for _, replicaSet := range replicaSets.Items {
				// skipping when it is a part of another service
				if len(replicaSet.GetOwnerReferences()) > 0 {
					continue
				}
				resources = append(resources, kuber.Resource{
					Kind:        "ReplicaSet",
					Annotations: replicaSet.Annotations,
					Namespace:   replicaSet.Namespace,
					Name:        replicaSet.Name,
					Containers:  replicaSet.Spec.Template.Spec.Containers,
					PodRegexp: regexp.MustCompile(
						fmt.Sprintf(
							"^%s-[^-]+$",
							regexp.QuoteMeta(replicaSet.Name),
						),
					),
					ReplicasStatus: proto.ReplicasStatus{
						Desired:   replicaSet.Spec.Replicas,
						Current:   newInt32Pointer(replicaSet.Status.Replicas),
						Ready:     newInt32Pointer(replicaSet.Status.ReadyReplicas),
						Available: newInt32Pointer(replicaSet.Status.AvailableReplicas),
					},
				})
			}
		}

		return nil
	})

	group.Go(func() error {
		cronJobs, err := kube.GetCronJobs()
		if err != nil {
			return karma.Format(
				err,
				"unable to get cron jobs",
			)
		}

		if cronJobs != nil {
			m.Lock()
			defer m.Unlock()

			rawResources["cronJobs"] = cronJobs

			for _, cronJob := range cronJobs.Items {
				activeCount := int32(len(cronJob.Status.Active))
				resources = append(resources, kuber.Resource{
					Kind:        "CronJob",
					Annotations: cronJob.Annotations,
					Namespace:   cronJob.Namespace,
					Name:        cronJob.Name,
					Containers:  cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers,
					PodRegexp: regexp.MustCompile(
						fmt.Sprintf(
							"^%s-[^-]+-[^-]+$",
							regexp.QuoteMeta(cronJob.Name),
						),
					),
					ReplicasStatus: proto.ReplicasStatus{
						Current: newInt32Pointer(activeCount),
					},
				})
			}
		}

		return nil
	})

	group.Go(func() error {
		limitRangeList, err := kube.GetLimitRanges()
		if err != nil {
			return karma.Format(
				err,
				"unable to get limitRanges",
			)
		}

		if limitRangeList != nil {
			limitRanges = limitRangeList.Items

			m.Lock()
			defer m.Unlock()

			rawResources["limitRanges"] = limitRangeList
		}

		return nil
	})

	err = group.Wait()

	return
}

func (kube *kubeObserver) GetReplicationControllers() (
	*corev1.ReplicationControllerList, error,
) {
	var items []corev1.ReplicationController
	err := kube.get(kuber.ReplicationControllers, &items)
	if err != nil {
		return nil, err
	}
	return &corev1.ReplicationControllerList{
		Items: items,
	}, nil
}

func (kube *kubeObserver) GetDeployments() (*appsv1beta2.DeploymentList, error) {
	var items []appsv1beta2.Deployment
	err := kube.get(kuber.Deployments, &items)
	if err != nil {
		return nil, err
	}
	return &appsv1beta2.DeploymentList{
		Items: items,
	}, nil
}

func (kube *kubeObserver) GetStatefulSets() (
	*appsv1beta2.StatefulSetList, error,
) {
	var items []appsv1beta2.StatefulSet
	err := kube.get(kuber.StatefulSets, &items)
	if err != nil {
		return nil, err
	}
	return &appsv1beta2.StatefulSetList{
		Items: items,
	}, nil
}

func (kube *kubeObserver) GetDaemonSets() (
	*appsv1beta2.DaemonSetList, error,
) {
	var items []appsv1beta2.DaemonSet
	err := kube.get(kuber.DaemonSets, &items)
	if err != nil {
		return nil, err
	}
	return &appsv1beta2.DaemonSetList{
		Items: items,
	}, nil
}

func (kube *kubeObserver) GetReplicaSets() (
	*appsv1beta2.ReplicaSetList, error,
) {
	var items []appsv1beta2.ReplicaSet
	err := kube.get(kuber.ReplicaSets, &items)
	if err != nil {
		return nil, err
	}
	return &appsv1beta2.ReplicaSetList{
		Items: items,
	}, nil
}

func (kube *kubeObserver) GetCronJobs() (
	*batchv1beta1.CronJobList, error,
) {
	var items []batchv1beta1.CronJob
	err := kube.get(kuber.CronJobs, &items)
	if err != nil {
		return nil, err
	}
	return &batchv1beta1.CronJobList{
		Items: items,
	}, nil
}

func (kube *kubeObserver) GetLimitRanges() (
	*corev1.LimitRangeList, error,
) {
	var items []corev1.LimitRange
	err := kube.get(kuber.LimitRanges, &items)
	if err != nil {
		return nil, err
	}
	return &corev1.LimitRangeList{
		Items: items,
	}, nil
}

func newInt32Pointer(val int32) *int32 {
	res := new(int32)
	*res = val
	return res
}
