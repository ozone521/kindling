package kubernetes

import (
	"fmt"
	appv1 "k8s.io/api/apps/v1"
	_ "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	_ "k8s.io/client-go/tools/clientcmd"
	_ "k8s.io/client-go/util/homedir"
	_ "path/filepath"
	"sync"
)

const(
	ReplicaSetKind = "ReplicaSet"
	ArmsAppEnable = "ArmsAppEnable"
	ArmsAppName = "ArmsAppName"
	ArmsAppDisabled = "off"
)

type ReplicaSetMap struct {
	// Key is ${namespace}/${name}
	Info map[string]*ReplicaSetInfo
	mut  sync.RWMutex
}

var globalRsInfo = newReplicaSetMap()
var rsUpdateMutex sync.Mutex

type ReplicaSetInfo struct {
	*Controller
	Labels map[string]string
}

type Controller struct {
	Name       string
	Kind       string
	APIVersion string
}

func newReplicaSetMap() *ReplicaSetMap {
	return &ReplicaSetMap{
		Info: make(map[string]*ReplicaSetInfo),
	}
}

func (rs *ReplicaSetMap) put(key string, rsi *ReplicaSetInfo) {
	rs.mut.Lock()
	rs.Info[key] = rsi
	rs.mut.Unlock()
}

func (rs *ReplicaSetMap) GetReplicaSetInfo(key string) (*ReplicaSetInfo, bool) {
	rs.mut.RLock()
	result, ok := rs.Info[key]
	rs.mut.RUnlock()
	return result, ok
}

func (rs *ReplicaSetMap) deleteOwnerReference(key string) {
	rs.mut.Lock()
	delete(rs.Info, key)
	rs.mut.Unlock()
}

func RsWatch(clientSet *kubernetes.Clientset) {
	stopper := make(chan struct{})
	defer close(stopper)

	factory := informers.NewSharedInformerFactory(clientSet, 0)
	rsInformer := factory.Apps().V1().ReplicaSets()
	informer := rsInformer.Informer()
	defer runtime.HandleCrash()

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    onAddReplicaSet,
		UpdateFunc: OnUpdateReplicaSet,
		DeleteFunc: onDeleteReplicaSet,
	})

	go factory.Start(stopper)

	if !cache.WaitForCacheSync(stopper, informer.HasSynced) {
		runtime.HandleError(fmt.Errorf("timed out waiting for caches to sync"))
		return
	}
	// TODO: use workqueue to avoid blocking
	<-stopper
}

func onAddReplicaSet(obj interface{}) {
	rs := obj.(*appv1.ReplicaSet)
	ownerRef := metav1.GetControllerOfNoCopy(rs)
	if ownerRef == nil {
		return
	}
	controller := &Controller{
		Name:       ownerRef.Name,
		Kind:       ownerRef.Kind,
		APIVersion: ownerRef.APIVersion,
	}
	replicaSetInfo := &ReplicaSetInfo{
		Controller: controller,
		Labels:     rs.Labels,
	}
	globalRsInfo.put(mapKey(rs.Namespace, rs.Name), replicaSetInfo)
}

func OnUpdateReplicaSet(objOld interface{}, objNew interface{}) {
	oldRs := objOld.(*appv1.ReplicaSet)
	newRs := objNew.(*appv1.ReplicaSet)
	if newRs.ResourceVersion == oldRs.ResourceVersion {
		return
	}
	rsUpdateMutex.Lock()
	// TODO: re-implement the updated logic to reduce computation
	onDeleteReplicaSet(objOld)
	onAddReplicaSet(objNew)
	rsUpdateMutex.Unlock()
}

func onDeleteReplicaSet(obj interface{}) {
	rs := obj.(*appv1.ReplicaSet)
	globalRsInfo.deleteOwnerReference(mapKey(rs.Namespace, rs.Name))
}

func createArmsApp(appId, language string) error {
	return nil
}
