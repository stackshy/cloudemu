'use client';

import Link from 'next/link';
import { useState } from 'react';
import { Copy, Check } from 'lucide-react';

export function CTASection() {
  const [copied, setCopied] = useState(false);
  const installCmd = 'go get github.com/stackshy/cloudemu';

  const handleCopy = async () => {
    await navigator.clipboard.writeText(installCmd);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <section className="w-full max-w-4xl mx-auto px-6 py-20 text-center">
      <h2 className="text-3xl font-bold mb-4">Ready to get started?</h2>
      <p className="text-fd-muted-foreground text-lg mb-8">
        Install cloudemu and start testing your cloud code in seconds
      </p>

      <div className="inline-flex items-center gap-3 px-6 py-4 rounded-xl border border-fd-border bg-fd-card font-mono text-sm mb-8">
        <span className="text-fd-muted-foreground">$</span>
        <span>{installCmd}</span>
        <button
          onClick={handleCopy}
          className="ml-2 p-1 rounded hover:bg-fd-secondary transition-colors"
          aria-label="Copy to clipboard"
        >
          {copied ? (
            <Check className="w-4 h-4 text-green-500" />
          ) : (
            <Copy className="w-4 h-4 text-fd-muted-foreground" />
          )}
        </button>
      </div>

      <div className="flex items-center justify-center gap-4 flex-wrap">
        <Link
          href="/docs"
          className="px-6 py-3 rounded-lg bg-fd-primary text-fd-primary-foreground font-semibold hover:opacity-90 transition-opacity"
        >
          Read the Docs
        </Link>
        <Link
          href="/docs/quick-start"
          className="px-6 py-3 rounded-lg border border-fd-border bg-fd-card font-semibold hover:bg-fd-accent transition-colors"
        >
          Quick Start Guide
        </Link>
      </div>

      <p className="mt-12 text-sm text-fd-muted-foreground">
        MIT License &middot; Requires Go 1.25+
      </p>
    </section>
  );
}
