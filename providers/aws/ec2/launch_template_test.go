package ec2

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// TestCreateLaunchTemplateVersionSequential pins that each template numbers its
// versions independently starting at 2 (v1 is the create), so two templates do
// not share a counter (regression for the old package-global atomic).
func TestCreateLaunchTemplateVersionSequential(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
		Name:           "web",
		InstanceConfig: driver.InstanceConfig{ImageID: "ami-1", InstanceType: "t2.micro"},
	})
	requireNoError(t, err)

	_, err = m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
		Name:           "db",
		InstanceConfig: driver.InstanceConfig{ImageID: "ami-9", InstanceType: "m5.large"},
	})
	requireNoError(t, err)

	v2, err := m.CreateLaunchTemplateVersion(ctx, driver.CreateLaunchTemplateVersionInput{
		Name:           "web",
		InstanceConfig: driver.InstanceConfig{ImageID: "ami-2", InstanceType: "t2.small"},
	})
	requireNoError(t, err)
	assertEqual(t, 2, v2.VersionNumber)

	v3, err := m.CreateLaunchTemplateVersion(ctx, driver.CreateLaunchTemplateVersionInput{
		Name:           "web",
		InstanceConfig: driver.InstanceConfig{ImageID: "ami-3"},
	})
	requireNoError(t, err)
	assertEqual(t, 3, v3.VersionNumber)

	// "db" must still be on its own version 1 -> its next version is 2, not 4.
	dbV2, err := m.CreateLaunchTemplateVersion(ctx, driver.CreateLaunchTemplateVersionInput{
		Name:           "db",
		InstanceConfig: driver.InstanceConfig{InstanceType: "m5.xlarge"},
	})
	requireNoError(t, err)
	assertEqual(t, 2, dbV2.VersionNumber)

	web, err := m.GetLaunchTemplate(ctx, "web")
	requireNoError(t, err)
	assertEqual(t, 3, web.LatestVersion)
	assertEqual(t, 1, web.DefaultVersion)
}

// TestCreateLaunchTemplateVersionSourceInherit pins that SourceVersion inherits
// the source's parameters and only the explicitly-set fields are overwritten.
func TestCreateLaunchTemplateVersionSourceInherit(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
		Name: "app",
		InstanceConfig: driver.InstanceConfig{
			ImageID:      "ami-base",
			InstanceType: "t2.micro",
			KeyName:      "my-key",
		},
	})
	requireNoError(t, err)

	// Overwrite only the instance type; ImageID and KeyName inherit from v1.
	v2, err := m.CreateLaunchTemplateVersion(ctx, driver.CreateLaunchTemplateVersionInput{
		Name:           "app",
		SourceVersion:  "1",
		InstanceConfig: driver.InstanceConfig{InstanceType: "c5.large"},
	})
	requireNoError(t, err)
	assertEqual(t, "ami-base", v2.InstanceConfig.ImageID)
	assertEqual(t, "my-key", v2.InstanceConfig.KeyName)
	assertEqual(t, "c5.large", v2.InstanceConfig.InstanceType)

	// Without a SourceVersion the new version inherits nothing.
	v3, err := m.CreateLaunchTemplateVersion(ctx, driver.CreateLaunchTemplateVersionInput{
		Name:           "app",
		InstanceConfig: driver.InstanceConfig{ImageID: "ami-fresh"},
	})
	requireNoError(t, err)
	assertEqual(t, "ami-fresh", v3.InstanceConfig.ImageID)
	assertEqual(t, "", v3.InstanceConfig.KeyName)
	assertEqual(t, "", v3.InstanceConfig.InstanceType)
}

// TestDescribeLaunchTemplateVersionsFilter pins explicit version selection and
// Min/Max bounds against a template with several versions.
func TestDescribeLaunchTemplateVersionsFilter(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
		Name:           "multi",
		InstanceConfig: driver.InstanceConfig{ImageID: "ami-1"},
	})
	requireNoError(t, err)

	for i := 2; i <= 5; i++ {
		_, verr := m.CreateLaunchTemplateVersion(ctx, driver.CreateLaunchTemplateVersionInput{
			Name:           "multi",
			InstanceConfig: driver.InstanceConfig{ImageID: "ami-x"},
		})
		requireNoError(t, verr)
	}

	all, err := m.DescribeLaunchTemplateVersions(ctx, driver.DescribeLaunchTemplateVersionsInput{Name: "multi"})
	requireNoError(t, err)
	assertEqual(t, 5, len(all))
	assertEqual(t, 1, all[0].VersionNumber)
	assertTrue(t, all[0].DefaultVersion, "version 1 is the default")

	only, err := m.DescribeLaunchTemplateVersions(ctx, driver.DescribeLaunchTemplateVersionsInput{
		Name:     "multi",
		Versions: []string{"2", "4"},
	})
	requireNoError(t, err)
	assertEqual(t, 2, len(only))
	assertEqual(t, 2, only[0].VersionNumber)
	assertEqual(t, 4, only[1].VersionNumber)

	bounded, err := m.DescribeLaunchTemplateVersions(ctx, driver.DescribeLaunchTemplateVersionsInput{
		Name:       "multi",
		MinVersion: "3",
		MaxVersion: "4",
	})
	requireNoError(t, err)
	assertEqual(t, 2, len(bounded))
	assertEqual(t, 3, bounded[0].VersionNumber)
	assertEqual(t, 4, bounded[1].VersionNumber)

	latest, err := m.DescribeLaunchTemplateVersions(ctx, driver.DescribeLaunchTemplateVersionsInput{
		Name:     "multi",
		Versions: []string{"$Latest"},
	})
	requireNoError(t, err)
	assertEqual(t, 1, len(latest))
	assertEqual(t, 5, latest[0].VersionNumber)
}

// TestGetLaunchTemplateDataFromInstance pins that launch-template data is
// synthesized from a running instance's configuration.
func TestGetLaunchTemplateDataFromInstance(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	instances, err := m.RunInstances(ctx, driver.InstanceConfig{
		ImageID:      "ami-run",
		InstanceType: "t3.medium",
		KeyName:      "kp",
	}, 1)
	requireNoError(t, err)

	cfg, err := m.GetLaunchTemplateData(ctx, instances[0].ID)
	requireNoError(t, err)
	assertEqual(t, "ami-run", cfg.ImageID)
	assertEqual(t, "t3.medium", cfg.InstanceType)
	assertEqual(t, "kp", cfg.KeyName)

	_, err = m.GetLaunchTemplateData(ctx, "i-missing")
	assertError(t, err, true)
	assertTrue(t, cerrors.IsNotFound(err), "missing instance is NotFound")
}

// TestCreateLaunchTemplateDuplicateIsAlreadyExists pins that a duplicate name
// yields an AlreadyExists error (the wire layer maps it to
// InvalidLaunchTemplateName.AlreadyExistsException).
func TestCreateLaunchTemplateDuplicateIsAlreadyExists(t *testing.T) {
	m := newTestMock()
	ctx := context.Background()

	_, err := m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
		Name:           "dup",
		InstanceConfig: driver.InstanceConfig{ImageID: "ami-1"},
	})
	requireNoError(t, err)

	_, err = m.CreateLaunchTemplate(ctx, driver.LaunchTemplateConfig{
		Name:           "dup",
		InstanceConfig: driver.InstanceConfig{ImageID: "ami-2"},
	})
	assertError(t, err, true)
	assertTrue(t, cerrors.IsAlreadyExists(err), "duplicate name is AlreadyExists")
}
