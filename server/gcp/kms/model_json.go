package kms

import (
	kmsprov "github.com/stackshy/cloudemu/v2/providers/gcp/kms"
)

// statePendingGeneration is the one version state with no key material yet.
const statePendingGeneration = "PENDING_GENERATION"

func toKeyRingJSON(kr *kmsprov.KeyRing) keyRingJSON {
	return keyRingJSON{Name: kr.Name, CreateTime: kmsprov.RFC3339(kr.CreateTime)}
}

func toCryptoKeyJSON(ck *kmsprov.CryptoKey) cryptoKeyJSON {
	out := cryptoKeyJSON{
		Name:                     ck.Name,
		Purpose:                  ck.Purpose,
		CreateTime:               kmsprov.RFC3339(ck.CreateTime),
		NextRotationTime:         ck.NextRotationTime,
		RotationPeriod:           ck.RotationPeriod,
		Labels:                   ck.Labels,
		ImportOnly:               ck.ImportOnly,
		DestroyScheduledDuration: ck.DestroyScheduledDuration,
		CryptoKeyBackend:         ck.CryptoKeyBackend,
	}

	if ck.Algorithm != "" || ck.ProtectionLevel != "" {
		out.VersionTemplate = &versionTemplateOut{
			ProtectionLevel: ck.ProtectionLevel,
			Algorithm:       ck.Algorithm,
		}
	}

	if ck.Primary != nil {
		p := toVersionJSON(ck.Name, ck.Primary)
		out.Primary = &p
	}

	return out
}

func toVersionJSON(parentKeyName string, v *kmsprov.Version) versionJSON {
	out := versionJSON{
		Name:            parentKeyName + "/cryptoKeyVersions/" + v.ID,
		State:           v.State,
		ProtectionLevel: v.ProtectionLevel,
		Algorithm:       v.Algorithm,
		CreateTime:      kmsprov.RFC3339(v.CreateTime),
	}

	// generateTime is present once key material exists; the emulator generates
	// synchronously, so it equals createTime for every non-pending version.
	if v.State != "" && v.State != statePendingGeneration {
		out.GenerateTime = out.CreateTime
	}

	if v.State == kmsprov.StateDestroyScheduled {
		out.DestroyTime = v.DestroyTime
	}

	if v.State == kmsprov.StateDestroyed {
		out.DestroyEventTime = v.DestroyEventTime
	}

	return out
}
