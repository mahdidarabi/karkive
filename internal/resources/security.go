package resources

import (
	corev1 "k8s.io/api/core/v1"

	karkivev1alpha1 "github.com/mahdidarabi/KArkive/api/v1alpha1"
	"github.com/mahdidarabi/KArkive/internal/ptr"
)

// PostgresUID is the CNPG / postgres image user (also the shared PVC fsGroup).
const PostgresUID int64 = 26

// MariaDBUID is the official mariadb image user.
const MariaDBUID int64 = 999

// RedisUID is the official redis image user.
const RedisUID int64 = 999

// McUID is the minio/mc image user.
const McUID int64 = 1000

// ToolsUID is used for busybox / gnupg stages (numeric; images have no dedicated user).
const ToolsUID int64 = 65532

func PodSecurityContext() *corev1.PodSecurityContext {
	return &corev1.PodSecurityContext{
		FSGroup:             ptr.To(PostgresUID),
		FSGroupChangePolicy: ptr.To(corev1.FSGroupChangeOnRootMismatch),
	}
}

func PostgresSecurityContext() *corev1.SecurityContext {
	return unixUserSecurityContext(PostgresUID)
}

func MariaDBSecurityContext() *corev1.SecurityContext {
	return unixUserSecurityContext(MariaDBUID)
}

func RedisSecurityContext() *corev1.SecurityContext {
	return unixUserSecurityContext(RedisUID)
}

func McSecurityContext() *corev1.SecurityContext {
	return unixUserSecurityContext(McUID)
}

func ToolsSecurityContext() *corev1.SecurityContext {
	return unixUserSecurityContext(ToolsUID)
}

// PVCDumpSecurityContext is for the tar stage of engine pvc. Default: root
// with only CAP_DAC_OVERRIDE, so tar reads files of any owner/mode from the
// read-only source mount (DAC_OVERRIDE, unlike DAC_READ_SEARCH, is allowed by
// Pod Security "baseline"). A non-zero spec.pvc.runAsUser runs as that UID
// with no capabilities instead ("restricted"-compatible).
func PVCDumpSecurityContext(src *karkivev1alpha1.PVCSourceSpec) *corev1.SecurityContext {
	var uid int64
	if src != nil && src.RunAsUser != nil {
		uid = *src.RunAsUser
	}
	gid := uid
	if src != nil && src.RunAsGroup != nil {
		gid = *src.RunAsGroup
	}
	if uid != 0 {
		sc := unixUserSecurityContext(uid)
		sc.RunAsGroup = ptr.To(gid)
		return sc
	}
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
			Add:  []corev1.Capability{"DAC_OVERRIDE"},
		},
		Privileged:             ptr.To(false),
		ReadOnlyRootFilesystem: ptr.To(true),
		RunAsGroup:             ptr.To(gid),
		RunAsNonRoot:           ptr.To(false),
		RunAsUser:              ptr.To(int64(0)),
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
}

func unixUserSecurityContext(uid int64) *corev1.SecurityContext {
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
		Privileged:             ptr.To(false),
		ReadOnlyRootFilesystem: ptr.To(true),
		RunAsGroup:             ptr.To(uid),
		RunAsNonRoot:           ptr.To(true),
		RunAsUser:              ptr.To(uid),
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
}

func DumpSecurityContext(engine karkivev1alpha1.Engine) *corev1.SecurityContext {
	switch EffectiveEngine(engine) {
	case karkivev1alpha1.EngineMariaDB:
		return MariaDBSecurityContext()
	case karkivev1alpha1.EngineRedis:
		return RedisSecurityContext()
	default:
		return PostgresSecurityContext()
	}
}
