/*


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

package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sampleoperatorv1alpha1 "github.com/maoqide/sample-operator/api/v1alpha1"
)

// FooReconciler reconciles a Foo object
type FooReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
	Cache  cache.Cache
}

const (
	// SuccessSynced is used as part of the Event 'reason' when a Foo is synced
	SuccessSynced = "Synced"
	// ErrResourceExists is used as part of the Event 'reason' when a Foo fails
	// to sync due to a Deployment of the same name already existing.
	ErrResourceExists = "ErrResourceExists"

	// MessageResourceExists is the message used for Events when a resource
	// fails to sync due to a Deployment already existing
	MessageResourceExists = "Resource %q already exists and is not managed by Foo"
	// MessageResourceSynced is the message used for an Event fired when a Foo
	// is synced successfully
	MessageResourceSynced = "Foo synced successfully"
)

// +kubebuilder:rbac:groups=sampleoperator.k8s.io,resources=foos,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sampleoperator.k8s.io,resources=foos/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployment,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployment/status,verbs=get

// Reconcile do sync
func (r *FooReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {

	ctx := context.Background()
	logger := r.Log.WithValues("foo", req.NamespacedName)

	logger.V(1).Info("foo reconcile begin...")
	var foo sampleoperatorv1alpha1.Foo
	if err := r.Get(ctx, req.NamespacedName, &foo); err != nil {
		logger.Error(err, "unable to fetch foo")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	deploymentName := foo.Spec.DeploymentName
	if deploymentName == "" {
		// We choose to absorb the error here as the worker would requeue the
		// resource otherwise. Instead, the next time the resource is updated
		// the resource will be queued again.
		return ctrl.Result{}, fmt.Errorf("BlankDeploymentName")
	}

	scheduledResult := ctrl.Result{RequeueAfter: time.Second * 5} // save this so we can re-use it elsewhere

	var deployment appsv1.Deployment
	if err := r.Get(ctx, namespacedName(req.Namespace, deploymentName), &deployment); err != nil {
		if errors.IsNotFound(err) {
			if err = r.Create(ctx, newDeployment(&foo)); err != nil {
				return scheduledResult, fmt.Errorf("FoosDeploymentCreateFailed")
			}
		} else {
			return scheduledResult, fmt.Errorf("FoosDeploymentNotFound")
		}
	}
	// If the Deployment is not controlled by this Foo resource, we should log
	// a warning to the event recorder and return error msg.
	if !metav1.IsControlledBy(&deployment, &foo) {
		msg := fmt.Sprintf(MessageResourceExists, deployment.Name)
		return ctrl.Result{}, fmt.Errorf(msg)
	}

	// If this number of the replicas on the Foo resource is specified, and the
	// number does not equal the current desired replicas on the Deployment, we
	// should update the Deployment resource.
	if foo.Spec.Replicas != nil && *foo.Spec.Replicas != *deployment.Spec.Replicas {
		logger.V(4).Info(fmt.Sprintf("Foo %s replicas: %d, deployment replicas: %d",
			req.NamespacedName, *foo.Spec.Replicas, *deployment.Spec.Replicas))
		if err := r.Update(ctx, newDeployment(&foo)); err != nil {
			return scheduledResult, fmt.Errorf("FoosDeploymentUpdateFailed")
		}
	}
	if err := r.updateFooStatus(ctx, &foo, &deployment); err != nil {
		logger.Error(err, "update foo status failed")
		return scheduledResult, fmt.Errorf("FoosUpdateStatusFailed")
	}
	return ctrl.Result{}, nil
}

// SetupWithManager setup
func (r *FooReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sampleoperatorv1alpha1.Foo{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}

func (r *FooReconciler) updateFooStatus(ctx context.Context, foo *sampleoperatorv1alpha1.Foo, deployment *appsv1.Deployment) error {
	// NEVER modify objects from the store. It's a read-only, local cache.
	// You can use DeepCopy() to make a deep copy of original object and modify this copy
	// Or create a copy manually for better performance
	fooCopy := foo.DeepCopy()
	fooCopy.Status.AvailableReplicas = deployment.Status.AvailableReplicas
	// If the CustomResourceSubresources feature gate is not enabled,
	// we must use Update instead of UpdateStatus to update the Status block of the Foo resource.
	// UpdateStatus will not allow changes to the Spec of the resource,
	// which is ideal for ensuring nothing other than resource status has been updated.
	return r.Status().Update(ctx, fooCopy)
}

func namespacedName(namespace, name string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}
}

func newDeployment(foo *sampleoperatorv1alpha1.Foo) *appsv1.Deployment {
	labels := map[string]string{
		"app":        "nginx",
		"controller": foo.Name,
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      foo.Spec.DeploymentName,
			Namespace: foo.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(foo, sampleoperatorv1alpha1.GroupVersion.WithKind("Foo")),
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: foo.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  fmt.Sprintf("%s-nginx", foo.Name),
							Image: "nginx:latest",
						},
					},
				},
			},
		},
	}
}
