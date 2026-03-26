import Link from 'next/link';
import { Hero } from '@/components/landing/hero';
import { ComparisonTable } from '@/components/landing/comparison-table';
import { FeatureCards } from '@/components/landing/feature-cards';
import { ServiceGrid } from '@/components/landing/service-grid';
import { CodeExample } from '@/components/landing/code-example';
import { CTASection } from '@/components/landing/cta-section';

export default function HomePage() {
  return (
    <main className="flex flex-col items-center">
      <Hero />

      {/* Provider Logos */}
      <section className="w-full max-w-6xl mx-auto px-6 py-12 text-center">
        <div className="flex items-center justify-center gap-8 flex-wrap">
          <div className="flex items-center gap-3 px-6 py-3 rounded-xl bg-fd-card border border-fd-border">
            <span className="text-2xl font-bold" style={{ color: '#FF9900' }}>AWS</span>
          </div>
          <div className="flex items-center gap-3 px-6 py-3 rounded-xl bg-fd-card border border-fd-border">
            <span className="text-2xl font-bold" style={{ color: '#0078D4' }}>Azure</span>
          </div>
          <div className="flex items-center gap-3 px-6 py-3 rounded-xl bg-fd-card border border-fd-border">
            <span className="text-2xl font-bold" style={{ color: '#4285F4' }}>GCP</span>
          </div>
        </div>
        <p className="mt-6 text-fd-muted-foreground text-lg">
          <span className="font-semibold text-fd-foreground">48 services</span> across 3 cloud providers — all in memory
        </p>
      </section>

      <ComparisonTable />
      <FeatureCards />
      <ServiceGrid />
      <CodeExample />
      <CTASection />
    </main>
  );
}
