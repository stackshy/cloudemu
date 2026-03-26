import Link from 'next/link';
import {
  Server, HardDrive, Database, Zap, Network, Activity,
  Shield, Globe, GitBranch, MessageSquare, Bell, Radio,
  Box, MemoryStick, Lock, FileText,
} from 'lucide-react';
import { services } from '@/lib/services';

const iconMap: Record<string, React.ComponentType<{ className?: string }>> = {
  Server, HardDrive, Database, Zap, Network, Activity,
  Shield, Globe, GitBranch, MessageSquare, Bell, Radio,
  Box, MemoryStick, Lock, FileText,
};

export function ServiceGrid() {
  return (
    <section className="w-full max-w-6xl mx-auto px-6 py-16">
      <h2 className="text-3xl font-bold text-center mb-2">16 Service Categories</h2>
      <p className="text-fd-muted-foreground text-center mb-10">
        Every category is implemented for AWS, Azure, and GCP
      </p>
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        {services.map((service) => {
          const Icon = iconMap[service.icon];
          return (
            <Link
              key={service.slug}
              href={`/docs/services/${service.slug}`}
              className="group rounded-xl border border-fd-border bg-fd-card p-5 hover:border-fd-primary/50 hover:shadow-md transition-all duration-200"
            >
              <div className="flex items-center gap-3 mb-3">
                {Icon && <Icon className="w-5 h-5 text-fd-primary" />}
                <h3 className="font-semibold">{service.category}</h3>
              </div>
              <div className="flex flex-col gap-1 text-xs text-fd-muted-foreground">
                <span>
                  <span className="font-medium" style={{ color: '#FF9900' }}>AWS</span>{' '}
                  {service.aws}
                </span>
                <span>
                  <span className="font-medium" style={{ color: '#0078D4' }}>Azure</span>{' '}
                  {service.azure}
                </span>
                <span>
                  <span className="font-medium" style={{ color: '#4285F4' }}>GCP</span>{' '}
                  {service.gcp}
                </span>
              </div>
            </Link>
          );
        })}
      </div>
    </section>
  );
}
