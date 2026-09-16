package resources

import (
	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
)

const (
	LabelAppName      = "app.kubernetes.io/name"
	LabelAppInstance  = "app.kubernetes.io/instance"
	LabelAppComponent = "app.kubernetes.io/component"
	LabelAppManagedBy = "app.kubernetes.io/managed-by"
	LabelAppPartOf    = "app.kubernetes.io/part-of"
	LabelBackupName   = "karkive.io/backup"
	LabelRestoreName  = "karkive.io/restore"
	LabelEngine       = "karkive.io/engine"
	LabelKind         = "karkive.io/kind"
	LabelRuntimeRole  = "karkive.io/runtime-role"

	ManagedBy = "karkive"
	PartOf    = "karkive"

	KindBackup  = "backup"
	KindRestore = "restore"
)

// BackupLabels returns labels applied to every resource owned by a Backup.
func BackupLabels(backup *karkivev1alpha1.Backup) map[string]string {
	component := backup.Spec.Component
	if component == "" {
		component = backup.Name
	}
	return map[string]string{
		LabelAppName:      backup.Name,
		LabelAppInstance:  backup.Name,
		LabelAppComponent: component,
		LabelAppManagedBy: ManagedBy,
		LabelAppPartOf:    PartOf,
		LabelBackupName:   backup.Name,
		LabelEngine:       string(EffectiveEngine(backup.Spec.Engine)),
		LabelKind:         KindBackup,
	}
}

// RestoreLabels returns labels applied to every resource owned by a Restore.
func RestoreLabels(restore *karkivev1alpha1.Restore) map[string]string {
	component := restore.Spec.Component
	if component == "" {
		component = restore.Name
	}
	return map[string]string{
		LabelAppName:      restore.Name,
		LabelAppInstance:  restore.Name,
		LabelAppComponent: component,
		LabelAppManagedBy: ManagedBy,
		LabelAppPartOf:    PartOf,
		LabelRestoreName:  restore.Name,
		LabelEngine:       string(EffectiveEngine(restore.Spec.Engine)),
		LabelKind:         KindRestore,
	}
}
