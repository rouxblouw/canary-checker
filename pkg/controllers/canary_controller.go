/*
Copyright 2020 The Kubernetes authors.

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
	"reflect"
	"strings"
	"sync"
	"time"

	canariesv1 "github.com/flanksource/canary-checker/api/v1"
	v1 "github.com/flanksource/canary-checker/api/v1"
	"github.com/flanksource/canary-checker/checks"
	"github.com/flanksource/canary-checker/pkg"
	"github.com/flanksource/canary-checker/pkg/cache"
	"github.com/flanksource/canary-checker/pkg/metrics"
	"github.com/go-logr/logr"
	"github.com/mitchellh/reflectwalk"
	"github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CanaryReconciler reconciles a Canary object
type CanaryReconciler struct {
	IncludeNamespace, IncludeCheck string
	client.Client
	Kubernetes kubernetes.Interface
	Log        logr.Logger
	Scheme     *runtime.Scheme
	Events     record.EventRecorder
	Cron       *cron.Cron
	Done       chan *pkg.CheckResult
}

func (r *CanaryReconciler) Report(key types.NamespacedName, results []*pkg.CheckResult) {
	check := v1.Canary{}
	if err := r.Get(context.TODO(), key, &check); err != nil {
		r.Log.Error(err, "unable to find canary", "key", key)
		return
	}

	check.Status.LastCheck = &metav1.Time{Time: time.Now()}
	transitioned := false
	pass := true
	for _, result := range results {
		lastResult := cache.AddCheck(fmt.Sprintf("%s/%s", key.Namespace, key.Name), result)
		metrics.Record(check.Namespace, check.Name, result)
		if lastResult != nil && len(lastResult.Statuses) > 0 && (lastResult.Statuses[0].Status != result.Pass) {
			transitioned = true
		}
		if !result.Pass {
			r.Events.Event(&check, corev1.EventTypeWarning, "Failed", fmt.Sprintf("%s-%s: %s", result.Check.GetType(), result.Check.GetEndpoint(), result.Message))
		}

		if transitioned {
			check.Status.LastTransitionedTime = &metav1.Time{Time: time.Now()}
		}
		pass = pass && result.Pass
	}
	if pass {
		check.Status.Status = &v1.Passed
	} else {
		check.Status.Status = &v1.Failed
	}
	r.Patch(check)
}

func (r *CanaryReconciler) Patch(canary v1.Canary) {
	r.Log.Info("patching", "canary", canary.Name, "namespace", canary.Namespace, "status", canary.Status.Status)

	if err := r.Status().Update(context.TODO(), &canary, &client.UpdateOptions{}); err != nil {
		r.Log.Error(err, "failed to patch", "canary", canary.Name)
	}
}

type CanaryJob struct {
	Client CanaryReconciler
	Check  v1.Canary
	logr.Logger
}

func (c CanaryJob) GetNamespacedName() types.NamespacedName {
	return types.NamespacedName{Name: c.Check.Name, Namespace: c.Check.Namespace}
}

func (c CanaryJob) Run() {

	spec, err := LoadSecrets(c.Client, c.Check)
	if err != nil {
		c.Error(err, "Failed to load secrets")
		return
	}
	c.Info("Starting")

	var results []*pkg.CheckResult
	for _, check := range checks.All {
		results = append(results, check.Run(spec)...)
	}

	c.Client.Report(c.GetNamespacedName(), results)

	c.Info("Ending")
}

type StructTemplater struct {
	Values map[string]string
}

// this func is required to fulfil the reflectwalk.StructWalker interface
func (w StructTemplater) Struct(reflect.Value) error {
	return nil
}

func (w StructTemplater) StructField(f reflect.StructField, v reflect.Value) error {
	if v.CanSet() && v.Kind() == reflect.String {
		v.SetString(w.Template(v.String()))
	}
	return nil
}

func (w StructTemplater) Template(val string) string {
	if strings.HasPrefix(val, "$") {
		key := strings.TrimRight(strings.TrimLeft(val[1:], "("), ")")
		env := w.Values[key]
		if env != "" {
			return env
		}
	}
	return val
}

func LoadSecrets(client CanaryReconciler, canary v1.Canary) (v1.CanarySpec, error) {
	var values = make(map[string]string)

	for key, source := range canary.Spec.Env {
		val, err := v1.GetEnvVarRefValue(client.Kubernetes, canary.Namespace, &source, &canary)
		if err != nil {
			return canary.Spec, err
		}
		values[key] = val
	}

	var val *v1.CanarySpec = &canary.Spec

	if err := reflectwalk.Walk(val, StructTemplater{Values: values}); err != nil {
		return canary.Spec, err
	}
	return *val, nil

}

// track the canaries that have already been scheduled
var observed = sync.Map{}

// +kubebuilder:rbac:groups=canaries.flanksource.com,resources=canaries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=canaries.flanksource.com,resources=canaries/status,verbs=get;update;patch
func (r *CanaryReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	if r.IncludeNamespace != "" && r.IncludeNamespace != req.Namespace {
		r.Log.V(2).Info("namespace not included, skipping")
		return ctrl.Result{}, nil
	}
	if r.IncludeCheck != "" && r.IncludeCheck != req.Name {
		r.Log.V(2).Info("check not included, skipping")
		return ctrl.Result{}, nil
	}

	ctx := context.Background()
	logger := r.Log.WithValues("canary", req.NamespacedName)

	check := v1.Canary{}
	if err := r.Get(ctx, req.NamespacedName, &check); err != nil {
		return ctrl.Result{}, err
	}

	_, run := observed.Load(req.NamespacedName)
	if run && check.Status.ObservedGeneration == check.Generation {
		logger.Info("check already up to date")
		return ctrl.Result{}, nil
	}

	observed.Store(req.NamespacedName, true)
	for _, entry := range r.Cron.Entries() {
		if entry.Job.(CanaryJob).GetNamespacedName() == req.NamespacedName {
			logger.Info("unscheduled", "id", entry.ID)
			r.Cron.Remove(entry.ID)
			break
		}
	}

	if check.Spec.Interval > 0 {
		job := CanaryJob{Client: *r, Check: check, Logger: logger}
		if !run {
			// check each job on startup
			go job.Run()
		}
		id, err := r.Cron.AddJob(fmt.Sprintf("@every %ds", check.Spec.Interval), job)
		if err != nil {
			logger.Error(err, "failed to schedule job", "schedule", check.Spec.Interval)
		} else {
			logger.Info("scheduled", "id", id, "next", r.Cron.Entry(id).Next)
		}
	}

	check.Status.ObservedGeneration = check.Generation
	r.Patch(check)

	return ctrl.Result{}, nil
}

func (r *CanaryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Events = mgr.GetEventRecorderFor("canary-checker")

	r.Cron = cron.New(cron.WithChain(
		cron.SkipIfStillRunning(r.Log),
	))
	r.Cron.Start()
	clientset, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		return err
	}
	r.Kubernetes = clientset

	return ctrl.NewControllerManagedBy(mgr).
		For(&canariesv1.Canary{}).
		Complete(r)
}
