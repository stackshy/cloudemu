'use client';

import { useState } from 'react';

const tabs = [
  {
    label: 'AWS',
    color: '#FF9900',
    code: `aws := cloudemu.NewAWS()

// Launch EC2 instances
instances, _ := aws.EC2.RunInstances(ctx, computedriver.InstanceConfig{
    ImageID:      "ami-0abcdef1234",
    InstanceType: "t3.large",
    Tags:         map[string]string{"env": "production"},
}, 3)

// Create S3 bucket and upload
aws.S3.CreateBucket(ctx, "app-data")
aws.S3.PutObject(ctx, "app-data", "config.yaml",
    []byte("port: 8080"), "text/yaml", nil)

// Push metrics to CloudWatch
aws.CloudWatch.PutMetricData(ctx, []mondriver.MetricDatum{
    {Namespace: "App", MetricName: "CPU", Value: 45.2},
})`,
  },
  {
    label: 'Azure',
    color: '#0078D4',
    code: `azure := cloudemu.NewAzure()

// Launch Virtual Machines
instances, _ := azure.VirtualMachines.RunInstances(ctx,
    computedriver.InstanceConfig{
        ImageID:      "Ubuntu-22.04",
        InstanceType: "Standard_D2s_v3",
        Tags:         map[string]string{"env": "production"},
    }, 3)

// Create Blob Storage container
azure.BlobStorage.CreateBucket(ctx, "app-data")
azure.BlobStorage.PutObject(ctx, "app-data", "config.yaml",
    []byte("port: 8080"), "text/yaml", nil)

// Push metrics to Azure Monitor
azure.Monitor.PutMetricData(ctx, []mondriver.MetricDatum{
    {Namespace: "App", MetricName: "CPU", Value: 45.2},
})`,
  },
  {
    label: 'GCP',
    color: '#4285F4',
    code: `gcp := cloudemu.NewGCP()

// Launch GCE instances
instances, _ := gcp.GCE.RunInstances(ctx,
    computedriver.InstanceConfig{
        ImageID:      "debian-11",
        InstanceType: "e2-standard-2",
        Tags:         map[string]string{"env": "production"},
    }, 3)

// Create GCS bucket
gcp.GCS.CreateBucket(ctx, "app-data")
gcp.GCS.PutObject(ctx, "app-data", "config.yaml",
    []byte("port: 8080"), "text/yaml", nil)

// Push metrics to Cloud Monitoring
gcp.CloudMonitoring.PutMetricData(ctx, []mondriver.MetricDatum{
    {Namespace: "App", MetricName: "CPU", Value: 45.2},
})`,
  },
];

export function CodeExample() {
  const [active, setActive] = useState(0);

  return (
    <section className="w-full max-w-4xl mx-auto px-6 py-16">
      <h2 className="text-3xl font-bold text-center mb-2">Same API, Every Provider</h2>
      <p className="text-fd-muted-foreground text-center mb-10">
        Switch providers by changing one line — your test code stays the same
      </p>
      <div className="rounded-xl border border-fd-border bg-fd-card overflow-hidden shadow-lg">
        <div className="flex border-b border-fd-border">
          {tabs.map((tab, i) => (
            <button
              key={tab.label}
              onClick={() => setActive(i)}
              className={`px-6 py-3 text-sm font-medium transition-colors ${
                active === i
                  ? 'bg-fd-card border-b-2 text-fd-foreground'
                  : 'text-fd-muted-foreground hover:text-fd-foreground hover:bg-fd-secondary/50'
              }`}
              style={active === i ? { borderBottomColor: tab.color } : undefined}
            >
              {tab.label}
            </button>
          ))}
        </div>
        <pre className="p-6 overflow-x-auto">
          <code className="text-sm font-mono text-fd-foreground leading-relaxed">
            {tabs[active].code}
          </code>
        </pre>
      </div>
    </section>
  );
}
