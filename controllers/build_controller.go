package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	opsv1alpha1 "kairos/api/v1alpha1"
)

// BuildReconciler 调和 Build 对象
type BuildReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=ops.kairos.io,resources=builds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ops.kairos.io,resources=builds/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ops.kairos.io,resources=builds/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *BuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// 1. 获取 Build 实例
	var build opsv1alpha1.Build
	if err := r.Get(ctx, req.NamespacedName, &build); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 2. 检查 Job 是否存在
	var job batchv1.Job
	jobName := fmt.Sprintf("build-%s", build.Name)
	err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: build.Namespace}, &job)

	// 如果 Job 不存在，则创建它
	if err != nil && errors.IsNotFound(err) {
		// 仅当构建尚未完成时才创建 Job
		if build.Status.Phase == opsv1alpha1.BuildPhaseSucceeded || build.Status.Phase == opsv1alpha1.BuildPhaseFailed {
			return ctrl.Result{}, nil
		}

		job, err := r.constructJob(&build)
		if err != nil {
			log.Error(err, "unable to construct job")
			return ctrl.Result{}, err
		}

		if err := r.Create(ctx, job); err != nil {
			log.Error(err, "unable to create job")
			return ctrl.Result{}, err
		}

		// 更新状态为 Running
		build.Status.Phase = opsv1alpha1.BuildPhaseRunning
		build.Status.JobRef = jobName
		if err := r.Status().Update(ctx, &build); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// 3. 根据 Job 状态更新 Status
	newPhase := build.Status.Phase
	var completionTime *metav1.Time

	if job.Status.Succeeded > 0 {
		newPhase = opsv1alpha1.BuildPhaseSucceeded
		completionTime = job.Status.CompletionTime
	} else if job.Status.Failed > 0 {
		newPhase = opsv1alpha1.BuildPhaseFailed
		completionTime = &metav1.Time{Time: time.Now()} // 如果 Job 完成时间为空，则使用当前时间作为后备
	}

	// 如果状态发生变化或需要回调
	if newPhase != build.Status.Phase {
		build.Status.Phase = newPhase
		build.Status.CompletionTime = completionTime

		if err := r.Status().Update(ctx, &build); err != nil {
			return ctrl.Result{}, err
		}

		// 处理回调和清理
		if newPhase == opsv1alpha1.BuildPhaseSucceeded || newPhase == opsv1alpha1.BuildPhaseFailed {
			if build.Spec.Callback != nil {
				if err := r.sendCallback(ctx, &build, newPhase); err != nil {
					log.Error(err, "failed to send callback")
					build.Status.CallbackStatus = "Failed"
					r.Status().Update(ctx, &build) // 尽力更新
				} else {
					build.Status.CallbackStatus = "Success"
					r.Status().Update(ctx, &build) // 尽力更新

					// 仅在回调成功且构建成功时自动清理
					if newPhase == opsv1alpha1.BuildPhaseSucceeded {
						log.Info("Build succeeded and callback sent, deleting CR", "name", build.Name)
						if err := r.Delete(ctx, &build); err != nil {
							log.Error(err, "failed to delete build CR")
							return ctrl.Result{}, err
						}
						// 对象已删除，直接返回
						return ctrl.Result{}, nil
					}
				}
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *BuildReconciler) constructJob(build *opsv1alpha1.Build) (*batchv1.Job, error) {
	jobName := fmt.Sprintf("build-%s", build.Name)
	privileged := true

	// 默认版本
	revision := build.Spec.Revision
	if revision == "" {
		revision = "master"
	}

	// 默认 Dockerfile
	dockerfile := build.Spec.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}

	// 构建构建脚本
	// 1. buildah bud
	// 2. buildah push
	buildScript := fmt.Sprintf(`
set -e
echo "Building image %s from %s..."
buildah bud --storage-driver=vfs -f %s -t %s .
echo "Pushing image..."
buildah push --storage-driver=vfs %s
echo "Done!"
`, build.Spec.OutputImage, build.Spec.ContextUrl, dockerfile, build.Spec.OutputImage, build.Spec.OutputImage)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: build.Namespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					InitContainers: []corev1.Container{
						{
							Name:    "git-clone",
							Image:   "alpine/git",
							Command: []string{"git", "clone", build.Spec.ContextUrl, "/workspace"},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "workspace",
									MountPath: "/workspace",
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:    "buildah",
							Image:   "quay.io/buildah/stable",
							Command: []string{"/bin/sh", "-c", buildScript},
							SecurityContext: &corev1.SecurityContext{
								Privileged: &privileged,
							},
							Env: []corev1.EnvVar{
								{
									Name:  "STORAGE_DRIVER",
									Value: "vfs",
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "workspace",
									MountPath: "/workspace",
								},
								{
									Name:      "containers-storage",
									MountPath: "/var/lib/containers",
								},
							},
							WorkingDir: "/workspace",
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "workspace",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "containers-storage",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
				},
			},
		},
	}

	// 如果提供了推送密钥，则添加它
	if build.Spec.PushSecret != "" {
		job.Spec.Template.Spec.Containers[0].VolumeMounts = append(job.Spec.Template.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
			Name:      "registry-auth",
			MountPath: "/root/.docker/config.json",
			SubPath:   ".dockerconfigjson",
			ReadOnly:  true,
		})
		job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes, corev1.Volume{
			Name: "registry-auth",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: build.Spec.PushSecret,
				},
			},
		})
	}

	// 设置 OwnerReference
	if err := controllerutil.SetControllerReference(build, job, r.Scheme); err != nil {
		return nil, err
	}

	return job, nil
}

func (r *BuildReconciler) sendCallback(ctx context.Context, build *opsv1alpha1.Build, phase opsv1alpha1.BuildPhase) error {
	payload := map[string]interface{}{
		"name":      build.Name,
		"namespace": build.Namespace,
		"phase":     phase,
		"image":     build.Spec.OutputImage,
		"timestamp": time.Now(),
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", build.Spec.Callback.URL, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	if build.Spec.Callback.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+build.Spec.Callback.AuthToken)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("callback failed with status code: %d", resp.StatusCode)
	}

	return nil
}

// SetupWithManager 使用 Manager 设置控制器。
func (r *BuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&opsv1alpha1.Build{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}
