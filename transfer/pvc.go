package transfer

import (
	corev1 "k8s.io/api/core/v1"
)

// PVC knows how to return v1.PersistentVolumeClaim and an additional validated
// name which can be used by different transfers as per their own requirements
type PVC interface {
	// Claim returns the v1.PersistentVolumeClaim reference this PVC is associated with
	Claim() *corev1.PersistentVolumeClaim
	// LabelSafeName returns a name for the PVC that can be used as a label value
	// it may be validated differently by different transfers
	LabelSafeName() string
}

type PVCList interface {
	Namespaces() []string
	InNamespace(ns string) PVCList
	PVCs() []PVC
}

// NamespaceHashForNames takes PVCList and returns a map with a unique md5 hash for each namespace
// based on the members in PVCList for that namespace.
func NamespaceHashForNames(pvcs PVCList) map[string]string {
	p := map[string]string{}
	for _, pvc := range pvcs.PVCs() {
		p[pvc.Claim().Namespace] += pvc.Claim().Name
	}
	for _, ns := range p {
		p[ns] = getMD5Hash(p[ns])
	}
	return p
}
