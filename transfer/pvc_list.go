package transfer

import (
	"crypto/md5"
	"encoding/hex"

	corev1 "k8s.io/api/core/v1"
)

// pvc represents a PersistentVolumeClaim
type pvc struct {
	p *corev1.PersistentVolumeClaim
}

var _ PVC = &pvc{}

// Claim returns ref to associated PersistentVolumeClaim
func (p pvc) Claim() *corev1.PersistentVolumeClaim {
	return p.p
}

// LabelSafeName returns a name which is guaranteed to be a safe label value
func (p pvc) LabelSafeName() string {
	return getMD5Hash(p.p.Name)
}

func getMD5Hash(s string) string {
	hash := md5.Sum([]byte(s))
	return hex.EncodeToString(hash[:])
}

// pvcList defines a managed list of PVCs
type pvcList []PVC

// NewPVCList when given a list of pvcs, returns a managed list
func NewPVCList(pvcs ...*corev1.PersistentVolumeClaim) (PVCList, error) {
	pvcList := pvcList{}
	for _, p := range pvcs {
		if p != nil {
			pvcList = append(pvcList, pvc{p})
		}
		// TODO: log an error here pvc list has an invalid entry
	}
	return pvcList, nil
}

// Namespaces returns all the namespaces present in the list of pvcs
func (p pvcList) Namespaces() (namespaces []string) {
	nsSet := map[string]bool{}
	for i := range p {
		pvc := p[i]
		if _, exists := nsSet[pvc.Claim().Namespace]; !exists {
			nsSet[pvc.Claim().Namespace] = true
			namespaces = append(namespaces, pvc.Claim().Namespace)
		}
	}
	return
}

// InNamespace given a destination namespace, returns a list of pvcs that will be migrated to it
func (p pvcList) InNamespace(ns string) PVCList {
	pvcList := pvcList{}
	for i := range p {
		pvc := p[i]
		if pvc.Claim().Namespace == ns {
			pvcList = append(pvcList, pvc)
		}
	}
	return pvcList
}

func (p pvcList) PVCs() []PVC {
	pvcs := []PVC{}
	for i := range p {
		if p[i].Claim() != nil {
			pvcs = append(pvcs, p[i])
		}
	}
	return pvcs
}

type singletonPVC struct {
	pvc *corev1.PersistentVolumeClaim
}

func (s singletonPVC) Claim() *corev1.PersistentVolumeClaim {
	return s.pvc
}

func (s singletonPVC) LabelSafeName() string {
	return "data"
}

func NewSingletonPVC(pvc *corev1.PersistentVolumeClaim) PVCList {
	if pvc != nil {
		return pvcList([]PVC{singletonPVC{pvc}})
	}
	return pvcList{}
}
