import {
  Workflow,
  Activity,
  AlertTriangle,
  Video,
  Clock,
  Package,
} from 'lucide-react';

const features = [
  {
    title: 'State Machines',
    description:
      'VMs enforce valid lifecycle transitions. Illegal state changes return errors, just like real clouds.',
    icon: Workflow,
  },
  {
    title: 'Auto-Metrics',
    description:
      'Launching a VM pushes CPU, Network, and Disk metrics to monitoring. Stop/terminate emit matching values.',
    icon: Activity,
  },
  {
    title: 'Error Injection',
    description:
      'Simulate failures deterministically: always, every Nth call, probabilistic, or first N calls.',
    icon: AlertTriangle,
  },
  {
    title: 'Call Recording',
    description:
      'Capture every API call with inputs, outputs, errors, and timing. Assert with fluent matchers.',
    icon: Video,
  },
  {
    title: 'Fake Clock',
    description:
      'Control time for deterministic testing of TTL, deduplication windows, and alarm evaluation.',
    icon: Clock,
  },
  {
    title: 'Zero Dependencies',
    description:
      'Pure Go with no external dependencies. Only testify for tests. Works anywhere Go runs.',
    icon: Package,
  },
];

export function FeatureCards() {
  return (
    <section className="w-full max-w-6xl mx-auto px-6 py-16">
      <h2 className="text-3xl font-bold text-center mb-2">Beyond Basic Mocks</h2>
      <p className="text-fd-muted-foreground text-center mb-10">
        cloudemu reproduces real cloud behaviors so your tests catch real issues
      </p>
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
        {features.map((feature) => (
          <div
            key={feature.title}
            className="group rounded-xl border border-fd-border bg-fd-card p-6 hover:shadow-lg hover:border-fd-primary/50 transition-all duration-200"
          >
            <div className="mb-4 inline-flex items-center justify-center w-10 h-10 rounded-lg bg-fd-primary/10 text-fd-primary">
              <feature.icon className="w-5 h-5" />
            </div>
            <h3 className="text-lg font-semibold mb-2">{feature.title}</h3>
            <p className="text-fd-muted-foreground text-sm leading-relaxed">
              {feature.description}
            </p>
          </div>
        ))}
      </div>
    </section>
  );
}
