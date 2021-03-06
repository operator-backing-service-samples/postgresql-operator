package database

import (
	"context"
	"strconv"

	postgresqlv1alpha1 "github.com/operator-backing-service-samples/postgresql-operator/pkg/apis/postgresql/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_database")

const (
	// ServiceName is the name of the service
	ServiceName = "postgresql"
	//DBHostKey is the config map key for DB host
	DBHostKey = "db.host"
	//DBPortKey is the config map key for DB port
	DBPortKey = "db.port"
	//DBUsernameKey is the config map key for DB username
	DBUsernameKey = "db.user"
	//DBPasswordKey is the config map key for DB password
	DBPasswordKey = "db.password"
	//DBNameKey is the config map key for DB name
	DBNameKey = "db.name"
)

// Add creates a new Database Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileDatabase{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("database-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource Database
	err = c.Watch(&source.Kind{Type: &postgresqlv1alpha1.Database{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource Pods and requeue the owner Database

	// Watch for Deployment Update and Delete event
	err = c.Watch(&source.Kind{Type: &appsv1.Deployment{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &postgresqlv1alpha1.Database{},
	})
	if err != nil {
		return err
	}

	// Watch for Service Update and Delete event
	err = c.Watch(&source.Kind{Type: &corev1.Service{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &postgresqlv1alpha1.Database{},
	})
	if err != nil {
		return err
	}

	// Watch for Secret Update and Delete event
	err = c.Watch(&source.Kind{Type: &corev1.Secret{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &postgresqlv1alpha1.Database{},
	})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileDatabase implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileDatabase{}

// ReconcileDatabase reconciles a Database object
type ReconcileDatabase struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a Database object and makes changes based on the state read
// and what is in the Database.Spec
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileDatabase) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling Database")

	data := map[string]string{}
	// Fetch the Database instance
	instance := &postgresqlv1alpha1.Database{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	data[DBNameKey] = dbName(instance)
	data[DBUsernameKey] = "postgres"
	data[DBPasswordKey] = "password"

	deployment := newDeploymentForCR(instance, data)

	// Set Database instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, deployment, r.scheme); err != nil {
		return reconcile.Result{}, err
	}

	// Check if this Deployment already exists
	deploymentFound := &appsv1.Deployment{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, deploymentFound)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new Deployment", "Deployment.Namespace", deployment.Namespace, "Deployment.Name", deployment.Name)
		err = r.client.Create(context.TODO(), deployment)
		if err != nil {
			return reconcile.Result{}, err
		}
		deploymentFound = deployment
		return reconcile.Result{Requeue: true}, nil
	} else if err != nil {
		return reconcile.Result{}, err
	}
	// Deployment created successfully - don't requeue
	instance.Status.DBName = data[DBNameKey]
	// Update status
	err = r.client.Status().Update(context.TODO(), instance)
	if err != nil {
		log.Error(err, "Failed to update status with DBName")
		return reconcile.Result{}, err
	}

	service := newServiceForCR(instance)
	// Set Database instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, service, r.scheme); err != nil {
		return reconcile.Result{}, err
	}

	// Check if this Service already exists
	serviceFound := &corev1.Service{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, serviceFound)
	if err != nil && errors.IsNotFound(err) { // service not found
		reqLogger.Info("Creating a new Service", "Service.Namespace", service.Namespace, "Service.Name", service.Name)
		err = r.client.Create(context.TODO(), service)
		if err != nil {
			return reconcile.Result{}, err
		}
		serviceFound = service
		return reconcile.Result{Requeue: true}, nil
	} else if err != nil {
		return reconcile.Result{}, err
	}
	instance.Status.DBConnectionIP = serviceFound.Spec.ClusterIP
	instance.Status.DBConnectionPort = int64(serviceFound.Spec.Ports[0].TargetPort.IntVal)
	data[DBHostKey] = instance.Status.DBConnectionIP
	data[DBPortKey] = strconv.FormatInt(instance.Status.DBConnectionPort, 10)
	// Update status
	err = r.client.Status().Update(context.TODO(), instance)
	if err != nil {
		log.Error(err, "Failed to update status with DBConnectionIP or DBConnectionPort ")
		return reconcile.Result{}, err
	}

	secret := newSecretForCR(instance, data)
	// Set Database instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, secret, r.scheme); err != nil {
		return reconcile.Result{}, err
	}
	// Check if this Secret already exists
	secretFound := &corev1.Secret{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}, secretFound)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new Secret", "Secret.Namespace", secret.Namespace, "Secret.Name", secret.Name)
		err = r.client.Create(context.TODO(), secret)
		if err != nil {
			return reconcile.Result{}, err
		}
		secretFound = secret
		return reconcile.Result{Requeue: true}, nil
	} else if err != nil {
		return reconcile.Result{}, err
	}
	// Secret created successfully update status with the reference
	instance.Status.DBCredentials = secretFound.Name
	// Update status
	err = r.client.Status().Update(context.TODO(), instance)
	if err != nil {
		log.Error(err, "Failed to update status with DBCredentials")
		return reconcile.Result{}, err
	}

	configMap := newConfigMapForCr(instance, data)
	// Set Database instance as the owner and controller
	if err := controllerutil.SetControllerReference(instance, configMap, r.scheme); err != nil {
		return reconcile.Result{}, err
	}
	// Check if this ConfigMap already exists
	configMapFound := &corev1.ConfigMap{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: configMap.Name, Namespace: configMap.Namespace}, configMapFound)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new ConfigMap", "ConfigMap.Namespace", configMap.Namespace, "ConfigMap.Name", configMap.Name)
		err = r.client.Create(context.TODO(), configMap)
		if err != nil {
			return reconcile.Result{}, err
		}
		configMapFound = configMap
	} else if err != nil {
		return reconcile.Result{}, err
	}
	// ConfigMap created successfully - update status with the reference
	instance.Status.DBConfigMap = configMapFound.Name
	// Update status
	err = r.client.Status().Update(context.TODO(), instance)
	if err != nil {
		log.Error(err, "Failed to update status with DBConfigMap")
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func newServiceForCR(cr *postgresqlv1alpha1.Database) *corev1.Service {
	labels := map[string]string{
		"app": cr.Name,
	}
	var svcPorts []corev1.ServicePort
	svcPort := corev1.ServicePort{
		Name:       cr.Name + "-postgresql",
		Port:       5432,
		Protocol:   corev1.ProtocolTCP,
		TargetPort: intstr.FromInt(5432),
	}
	svcPorts = append(svcPorts, svcPort)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-postgresql",
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Ports: svcPorts,
			Selector: map[string]string{
				"app": cr.Name,
			},
		},
	}
	return svc
}

func newSecretForCR(cr *postgresqlv1alpha1.Database, data map[string]string) *corev1.Secret {
	labels := map[string]string{
		"app": cr.Name,
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-postgresql",
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"user":     []byte(data[DBUsernameKey]),
			"password": []byte(data[DBPasswordKey]),
		},
	}
	return secret
}

func newConfigMapForCr(cr *postgresqlv1alpha1.Database, data map[string]string) *corev1.ConfigMap {
	labels := map[string]string{
		"app": cr.Name,
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name,
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Data: data,
	}
	return configMap
}

func newDeploymentForCR(cr *postgresqlv1alpha1.Database, data map[string]string) *appsv1.Deployment {
	labels := map[string]string{
		"app": cr.Name,
	}
	containerPorts := []corev1.ContainerPort{{
		ContainerPort: 5432,
		Protocol:      corev1.ProtocolTCP,
	}}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-postgresql",
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Strategy: appsv1.DeploymentStrategy{
				Type: "Recreate",
			},
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cr.Name + "-postgresql",
					Namespace: cr.Namespace,
					Labels:    labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            cr.Spec.ImageName,
							Image:           cr.Spec.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Ports:           containerPorts,
							Env: []corev1.EnvVar{
								{
									Name:  "POSTGRES_USER",
									Value: data[DBUsernameKey],
								},
								{
									Name:  "POSTGRES_PASSWORD",
									Value: data[DBPasswordKey],
								},
								{
									Name:  "POSTGRES_DB",
									Value: data[DBNameKey],
								},
								{
									Name:  "PGDATA",
									Value: "/var/lib/postgresql/data/pgdata",
								},
							},
						},
					},
				},
			},
		},
	}
	return deployment
}

func dbName(cr *postgresqlv1alpha1.Database) string {
	if cr.Spec.DBName != "" {
		return cr.Spec.DBName
	}
	return "postgres"
}

func int32Ptr(i int32) *int32 {
	return &i
}
