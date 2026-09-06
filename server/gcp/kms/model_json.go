package kms

// cryptoKeyConfig is the validated, normalized input for creating a CryptoKey.
type cryptoKeyConfig struct {
	id                       string
	purpose                  string
	rotationPeriod           string
	nextRotationTime         string
	protectionLevel          string
	algorithm                string
	labels                   map[string]string
	importOnly               bool
	destroyScheduledDuration string
	cryptoKeyBackend         string
}

// cryptoKeyPatch is a normalized CryptoKey patch: each set* flag marks a field
// the update mask named, so an omitted field is left untouched.
type cryptoKeyPatch struct {
	labels              map[string]string
	setLabels           bool
	rotationPeriod      string
	setRotationPeriod   bool
	nextRotationTime    string
	setNextRotationTime bool
	protectionLevel     string
	setProtectionLevel  bool
	algorithm           string
	setAlgorithm        bool
}

func toKeyRingJSON(kr *keyRingModel) keyRingJSON {
	return keyRingJSON{Name: kr.name, CreateTime: rfc3339(kr.createTime)}
}

func toCryptoKeyJSON(ck *cryptoKeyModel) cryptoKeyJSON {
	out := cryptoKeyJSON{
		Name:                     ck.name,
		Purpose:                  ck.purpose,
		CreateTime:               rfc3339(ck.createTime),
		NextRotationTime:         ck.nextRotationTime,
		RotationPeriod:           ck.rotationPeriod,
		Labels:                   ck.labels,
		ImportOnly:               ck.importOnly,
		DestroyScheduledDuration: ck.destroyScheduledDuration,
		CryptoKeyBackend:         ck.cryptoKeyBackend,
	}

	if ck.algorithm != "" || ck.protectionLevel != "" {
		out.VersionTemplate = &versionTemplateOut{
			ProtectionLevel: ck.protectionLevel,
			Algorithm:       ck.algorithm,
		}
	}

	if ck.primaryID != "" {
		if v, ok := ck.versions[ck.primaryID]; ok {
			p := toVersionJSON(ck.name, v)
			out.Primary = &p
		}
	}

	return out
}

func toVersionJSON(parentKeyName string, v *versionModel) versionJSON {
	out := versionJSON{
		Name:            parentKeyName + "/cryptoKeyVersions/" + v.id,
		State:           v.state,
		ProtectionLevel: v.protectionLevel,
		Algorithm:       v.algorithm,
		CreateTime:      rfc3339(v.createTime),
	}

	// generateTime is present once key material exists; the emulator generates
	// synchronously, so it equals createTime for every non-pending version.
	if v.state != "" && v.state != "PENDING_GENERATION" {
		out.GenerateTime = rfc3339(v.createTime)
	}

	if v.state == stateDestroyScheduled {
		out.DestroyTime = v.destroyTime
	}

	if v.state == stateDestroyed {
		out.DestroyEventTime = v.destroyEventTime
	}

	return out
}
