package controller

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/appscode/go/log"
	dynamicclientset "github.com/appscode/kutil/dynamic/clientset"
	dynamicdiscovery "github.com/appscode/kutil/dynamic/discovery"
	dynamicinformer "github.com/appscode/kutil/dynamic/informer"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ktypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/jsonpath"
	api "kube.ci/kubeci/apis/kubeci/v1alpha1"
)

type ResourceIdentifier struct {
	Object            map[string]interface{}
	Name              string
	ApiVersion        string
	Group             string
	Version           string
	Kind              string
	Resource          string
	Namespace         string
	UID               ktypes.UID
	Generation        int64
	DeletionTimestamp *metav1.Time
	Labels            map[string]string
}

func (res ResourceIdentifier) String() string {
	return fmt.Sprintf("%s/%s:%s/%s", res.ApiVersion, res.Kind, res.Namespace, res.Name)
}

func (res ResourceIdentifier) GetData(paths map[string]string) map[string]string {
	data := make(map[string]string, 0)
	for env, path := range paths {
		data[env] = jsonPathData(path, res.Object)
	}
	return data
}

func jsonPathData(path string, data interface{}) string {
	j := jsonpath.New("kubeci")
	j.AllowMissingKeys(true) // TODO: true or false ? ignore errors ?

	if err := j.Parse(path); err != nil {
		return ""
	}

	buf := new(bytes.Buffer)
	if err := j.Execute(buf, data); err != nil {
		return ""
	}

	return buf.String()
}

func objToResourceIdentifier(obj interface{}) ResourceIdentifier {
	o := obj.(*unstructured.Unstructured)

	apiVersion := o.GetAPIVersion()
	group, version := toGroupAndVersion(apiVersion)

	return ResourceIdentifier{
		Object:            o.Object,
		ApiVersion:        apiVersion,
		Group:             group,
		Version:           version,
		Kind:              o.GetKind(),
		Resource:          selfLinkToResource(o.GetSelfLink()),
		Namespace:         o.GetNamespace(),
		Name:              o.GetName(),
		UID:               o.GetUID(),
		Generation:        o.GetGeneration(),
		Labels:            o.GetLabels(),
		DeletionTimestamp: o.GetDeletionTimestamp(),
	}
}

// TODO: how to get resource from unstructured object
func selfLinkToResource(selfLink string) string {
	items := strings.Split(selfLink, "/")
	return items[len(items)-2]
}

func toGroupAndVersion(apiVersion string) (string, string) {
	items := strings.Split(apiVersion, "/")
	if len(items) == 1 {
		return "", items[0]
	}
	return items[0], items[1]
}

func (c *Controller) initDynamicWatcher() {
	// resync periods
	discoveryInterval := 5 * time.Second
	informerRelist := 5 * time.Minute

	// Periodically refresh discovery to pick up newly-installed resources.
	dc := discovery.NewDiscoveryClientForConfigOrDie(c.clientConfig)
	resources := dynamicdiscovery.NewResourceMap(dc)
	// We don't care about stopping this cleanly since it has no external effects.
	resources.Start(discoveryInterval)

	// Create dynamic clientset (factory for dynamic clients).
	c.dynClient = dynamicclientset.New(c.clientConfig, resources)
	// Create dynamic informer factory (for sharing dynamic informers).
	c.dynInformersFactory = dynamicinformer.NewSharedInformerFactory(c.dynClient, informerRelist)

	log.Infof("Waiting for sync")
	if !resources.HasSynced() {
		time.Sleep(time.Second)
	}
}

func (c *Controller) createInformer(wf *api.Workflow) error {
	for _, trigger := range wf.Spec.Triggers {
		informer, err := c.dynInformersFactory.Resource(trigger.ApiVersion, trigger.Resource)
		if err != nil {
			return err
		}
		informer.Informer().AddEventHandler(c.handlerForDynamicInformer())
		log.Infof("Created informer for resource %s/%s", trigger.ApiVersion, trigger.Resource)
	}
	return nil
}

// common handler for all events
func (c *Controller) handlerForDynamicInformer() cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			res := objToResourceIdentifier(obj)
			log.Debugln("Added resource", res)
			c.handleTrigger(res)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			res := objToResourceIdentifier(newObj)
			log.Debugln("Updated resource", res)
			c.handleTrigger(res)
		},
		DeleteFunc: func(obj interface{}) {
			res := objToResourceIdentifier(obj)
			log.Debugln("Deleted resource", res)
			c.handleTrigger(res)
		},
	}
}