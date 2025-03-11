package loadtest

import (
	v1 "k8s.io/api/apps/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/client-go/kubernetes"
)

func Apply(client kubernetes.Interface, obj kubernetes.Interface) error {
	return nil
}

// DeploymentWithUnsatisfiablePDB generates a 1-replica deployment and a PDB that does not allow any disruption.
// Useful to test "stuck" drains.
func DeploymentWithUnsatisfiablePDB(client kubernetes.Interface) (*v1.Deployment, *policyv1.PodDisruptionBudget) {
	//a := v1.Deployment{
	//	TypeMeta:   metav1.TypeMeta{},
	//	ObjectMeta: metav1.ObjectMeta{},
	//	Spec:       v1.DeploymentSpec{},
	//	Status:     v1.DeploymentStatus{},
	//}
	//b := policyv1.PodDisruptionBudget{}
	//
	////Spec.
	//
	//v1.De
	return nil, nil
}
