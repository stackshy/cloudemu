package redshift

// eventInfo is one entry of the Redshift event catalog.
type eventInfo struct {
	id, category, severity, description string
}

// eventCatalogSourceTypes fixes the order DescribeEventCategories lists
// source types in.
//
//nolint:gochecknoglobals // static lookup table
var eventCatalogSourceTypes = []string{"cluster", "cluster-parameter-group", "cluster-security-group", "cluster-snapshot"}

// eventCatalog holds events from the Redshift event notification reference.
// The parameter group, security group and snapshot lists are complete. The
// cluster list covers the lifecycle cloudemu models, not every published event.
//
//nolint:gochecknoglobals // static lookup table
var eventCatalog = map[string][]eventInfo{
	"cluster": {
		{"REDSHIFT-EVENT-1000", "configuration", "INFO", "The parameter group [parameter group name] was updated at [time]. " +
			"If you changed only dynamic parameters, associated clusters are being modified now. If you changed static parameters, " +
			"all updates, including dynamic parameters, will be applied when you reboot the associated clusters."},
		{"REDSHIFT-EVENT-1001", "configuration", "INFO",
			"Your Amazon Redshift cluster [cluster name] was modified to use parameter group [parameter group name] at [time]."},
		{"REDSHIFT-EVENT-2000", "management", "INFO",
			"Your Amazon Redshift cluster: [cluster name] has been created and is ready for use."},
		{"REDSHIFT-EVENT-2001", "management", "INFO",
			"Your Amazon Redshift cluster [cluster name] was deleted at [time]. A final snapshot [was / was not] saved."},
		{"REDSHIFT-EVENT-2002", "management", "INFO", "VPC security groups for cluster [cluster name] updated at [time in UTC]."},
		{"REDSHIFT-EVENT-2003", "management", "INFO", "Maintenance started on cluster [cluster name] at [time in UTC]."},
		{"REDSHIFT-EVENT-2004", "management", "INFO", "Maintenance on cluster [cluster name] completed at [time in UTC]."},
		{"REDSHIFT-EVENT-2006", "management", "INFO",
			"Cluster [cluster name] resize started at [time in UTC]. Cluster is in read-only mode."},
		{"REDSHIFT-EVENT-2008", "management", "INFO", "Your restore operation to create a new Amazon Redshift cluster [cluster name] " +
			"snapshot [snapshot name] was started at [time]. To monitor restore progress, please visit the AWS Management Console."},
		{"REDSHIFT-EVENT-2025", "pending", "INFO", "Your database for cluster [cluster name] will be updated between [start time] " +
			"and [end time]. Your cluster will not be accessible. Plan accordingly."},
		{"REDSHIFT-EVENT-3000", "monitoring", "INFO", "Your Amazon Redshift cluster [cluster name] was rebooted at [time]."},
		{"REDSHIFT-EVENT-3002", "monitoring", "INFO", "The resize for your Amazon Redshift cluster [cluster name] is complete and " +
			"your cluster is available for reads and writes. The resize was initiated at [time] and took [hours] hours to complete."},
		{"REDSHIFT-EVENT-3003", "monitoring", "INFO",
			"Amazon Redshift cluster [cluster name] was successfully created from snapshot [snapshot name] and is available for use."},
		{"REDSHIFT-EVENT-3618", "monitoring", "INFO", "The cluster [cluster name] pause operation started at [UTC time]. Pause Started"},
		{"REDSHIFT-EVENT-3619", "monitoring", "INFO", "Amazon Redshift cluster [cluster name] was successfully paused at [UTC time]."},
		{"REDSHIFT-EVENT-4000", "security", "INFO",
			"Your admin credentials for your Amazon Redshift cluster: [cluster name] were updated at [time]."},
		{"REDSHIFT-EVENT-4001", "security", "INFO", "The security group [security group name] was modified at [time]. " +
			"The changes will take place for all associated clusters automatically."},
	},
	"cluster-parameter-group": {
		{"REDSHIFT-EVENT-1002", "configuration", "INFO",
			"The parameter [parameter name] was updated from [value] to [value] at [time]."},
		{"REDSHIFT-EVENT-1003", "configuration", "INFO", "Cluster parameter group [group name] was created."},
		{"REDSHIFT-EVENT-1004", "configuration", "INFO", "Cluster parameter group [group name] was deleted."},
		{"REDSHIFT-EVENT-1005", "configuration", "INFO", "Cluster parameter group [name] was updated at [time]. " +
			"If you changed only dynamic parameters, associated clusters are being modified now. If you changed static parameters, " +
			"all updates, including dynamic parameters, will be applied when you reboot the associated clusters."},
	},
	"cluster-security-group": {
		{"REDSHIFT-EVENT-4002", "security", "INFO", "Cluster security group [group name] was created."},
		{"REDSHIFT-EVENT-4003", "security", "INFO", "Cluster security group [group name] was deleted."},
		{"REDSHIFT-EVENT-4004", "security", "INFO", "Cluster security group [group name] was changed at [time]. " +
			"Changes will be automatically applied to all associated clusters."},
	},
	"cluster-snapshot": {
		{"REDSHIFT-EVENT-2009", "management", "INFO", "A user snapshot [snapshot name] for Amazon Redshift Cluster [cluster name] " +
			"started at [time]. To monitor snapshot progress, please visit the AWS Management Console."},
		{"REDSHIFT-EVENT-2010", "management", "INFO",
			"The user snapshot [snapshot name] for your Amazon Redshift cluster [cluster name] " +
				"was cancelled at [time]."}, //nolint:misspell // verbatim AWS text
		{"REDSHIFT-EVENT-2011", "management", "INFO",
			"The user snapshot [snapshot name] for Amazon Redshift cluster [cluster name] was deleted at [time]."},
		{"REDSHIFT-EVENT-2012", "management", "INFO",
			"The final snapshot [snapshot name] for Amazon Redshift cluster [cluster name] was started at [time]."},
		{"REDSHIFT-EVENT-3004", "monitoring", "INFO",
			"The user snapshot [snapshot name] for your Amazon Redshift cluster [cluster name] completed successfully at [time]."},
		{"REDSHIFT-EVENT-3005", "monitoring", "INFO",
			"The final snapshot [name] for Amazon Redshift cluster [name] completed successfully at [time]."},
		{"REDSHIFT-EVENT-3006", "monitoring", "INFO",
			"The final snapshot [snapshot name] for Amazon Redshift cluster [cluster name] " +
				"was cancelled at [time]."}, //nolint:misspell // verbatim AWS text
		{"REDSHIFT-EVENT-3502", "monitoring", "ERROR", "The final snapshot [snapshot name] for Amazon Redshift cluster " +
			"[cluster name] failed at [time]. The team is investigating the issue. " +
			"Please visit the AWS Management Console to retry the operation."},
		{"REDSHIFT-EVENT-3503", "monitoring", "ERROR", "The user snapshot [snapshot name] for your Amazon Redshift cluster " +
			"[cluster name] failed at [time]. The team is investigating the issue. " +
			"Please visit the AWS Management Console to retry the operation."},
	},
}
