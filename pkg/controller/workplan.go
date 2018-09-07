package controller

import (
	"github.com/appscode/go/log"
	"github.com/appscode/kutil/tools/queue"
	api "kube.ci/kubeci/apis/kubeci/v1alpha1"
	"kube.ci/kubeci/client/clientset/versioned/typed/kubeci/v1alpha1/util"
)

// process only add and delete events
// uninitialized: newly created
// running: previously created, but operator restarted before it succeeded

func (c *Controller) initWorkplanWatcher() {
	c.wpInformer = c.kubeciInformerFactory.Kubeci().V1alpha1().Workplans().Informer()
	c.wpQueue = queue.New("Workplan", c.MaxNumRequeues, c.NumThreads, c.runWorkplanInjector)
	c.wpInformer.AddEventHandler(queue.NewEventHandler(c.wpQueue.GetQueue(), func(oldObj, newObj interface{}) bool {
		return false
	}))
	c.wpLister = c.kubeciInformerFactory.Kubeci().V1alpha1().Workplans().Lister()
}

func (c *Controller) runWorkplanInjector(key string) error {
	obj, exist, err := c.wpInformer.GetIndexer().GetByKey(key)
	if err != nil {
		log.Errorf("Fetching object with key %s from store failed with %v", key, err)
		return err
	}

	if !exist {
		log.Warningf("Workplan %s does not exist anymore\n", key)
	} else {
		wp := obj.(*api.Workplan).DeepCopy()
		if wp.Status.Phase == api.WorkplanUninitialized || wp.Status.Phase == api.WorkplanRunning {
			log.Infof("Sync/Add/Update for workplan %s", key)
			if err := c.reconcileForWorkplan(wp); err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *Controller) reconcileForWorkplan(wp *api.Workplan) error {
	go c.executeWorkplan(wp)
	return nil
}

func (c *Controller) executeWorkplan(wp *api.Workplan) {
	var err error
	if wp.Status.Phase == api.WorkplanUninitialized {
		log.Infof("Executing workplan %s", wp.Name)
		if wp, err = util.UpdateWorkplanStatus(
			c.kubeciClient.KubeciV1alpha1(),
			wp,
			func(r *api.WorkplanStatus) *api.WorkplanStatus {
				r.Phase = api.WorkplanPending
				r.TaskIndex = -1
				r.Reason = "Initializing tasks"
				return r
			},
			api.EnableStatusSubresource,
		); err != nil {
			log.Errorf(err.Error())
			return
		}
	} else if wp.Status.Phase == api.WorkplanRunning {
		log.Infof("Resuming workplan %s", wp.Name)
	}

	if err = c.runTasks(wp); err != nil {
		log.Errorf("Failed to execute workplan: %s, reason: %s", wp.Name, err.Error())
		if wp, err = util.UpdateWorkplanStatus(
			c.kubeciClient.KubeciV1alpha1(),
			wp,
			func(r *api.WorkplanStatus) *api.WorkplanStatus {
				r.Phase = api.WorkplanFailed
				r.TaskIndex = -1
				r.Reason = err.Error()
				return r
			},
			api.EnableStatusSubresource,
		); err != nil {
			log.Errorf(err.Error())
		}
		return
	}

	log.Infof("Workplan %s completed successfully", wp.Name)
	if wp, err = util.UpdateWorkplanStatus(
		c.kubeciClient.KubeciV1alpha1(),
		wp,
		func(r *api.WorkplanStatus) *api.WorkplanStatus {
			r.Phase = api.WorkplanSucceeded
			r.TaskIndex = -1
			r.Reason = "All tasks completed successfully"
			return r
		},
		api.EnableStatusSubresource,
	); err != nil {
		log.Errorf(err.Error())
	}
}
