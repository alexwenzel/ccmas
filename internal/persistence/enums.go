package persistence

import (
	"github.com/alexwenzel/ccmas/internal/skills"
	"github.com/alexwenzel/ccmas/internal/supervisor"
)

// Rein interne Hilfslisten: Spiegel von Typen aus supervisor und skills, die dort nicht als Liste vorliegen. Domain-Typen
// (auch Reservierungs-, Zustell- und Outbox-Zustände) stehen in internal/domain. Die CHECK-Listen der Migration 001
// entsprechen genau diesen Listen und denen der Domain (TestEnumChecksMatchDomain).

// prStates, ciStates, conceptClasses und approvers sind die Werte aus supervisor und skills (dort ohne Listenfunktion).
func prStates() []supervisor.PRStatus {
	return []supervisor.PRStatus{supervisor.PROpen, supervisor.PRMerged, supervisor.PRClosed}
}

func ciStates() []supervisor.CIStatus {
	return []supervisor.CIStatus{supervisor.CIPassed, supervisor.CIFailed, supervisor.CIPending, supervisor.CIMissing}
}

func conceptClasses() []skills.ConceptClass {
	return []skills.ConceptClass{skills.ConceptRoutine, skills.ConceptStrategic}
}

func approvers() []skills.Approver {
	return []skills.Approver{skills.ApproverConceptReviewer, skills.ApproverUser}
}

func integrationStates() []skills.IntegrationState {
	return []skills.IntegrationState{skills.IntegrationNone, skills.IntegrationPROpen, skills.IntegrationMerged}
}

func mergeActors() []skills.MergeActor {
	return []skills.MergeActor{"", skills.MergedByHuman, skills.MergedByQueue}
}

func has[T comparable](list []T, v T) bool {
	for _, e := range list {
		if e == v {
			return true
		}
	}
	return false
}

func conceptClassOf(s string) skills.ConceptClass { return skills.ConceptClass(s) }

func approverOf(s string) skills.Approver { return skills.Approver(s) }
