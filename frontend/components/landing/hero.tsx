'use client';

import Link from 'next/link';
import { motion } from 'framer-motion';

export function Hero() {
  return (
    <section className="w-full max-w-6xl mx-auto px-6 pt-24 pb-16 text-center">
      <motion.div
        initial={{ opacity: 0, y: 20 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.5 }}
      >
        <div className="inline-flex items-center gap-2 px-4 py-1.5 mb-6 rounded-full border border-fd-border bg-fd-card text-sm text-fd-muted-foreground">
          <span className="inline-block w-2 h-2 rounded-full bg-green-500" />
          Open Source &middot; MIT License
        </div>

        <h1 className="text-5xl sm:text-6xl lg:text-7xl font-extrabold tracking-tight leading-tight">
          Zero-Cost{' '}
          <span className="bg-gradient-to-r from-blue-500 via-purple-500 to-pink-500 bg-clip-text text-transparent">
            Cloud Emulation
          </span>
          <br />
          for Go
        </h1>

        <p className="mt-6 text-xl text-fd-muted-foreground max-w-2xl mx-auto leading-relaxed">
          No cloud accounts. No Docker. No network calls.
          <br />
          Just <code className="px-2 py-0.5 rounded bg-fd-card border border-fd-border text-sm font-mono">go get</code> and test.
        </p>

        <div className="mt-10 flex items-center justify-center gap-4 flex-wrap">
          <Link
            href="/docs/quick-start"
            className="px-6 py-3 rounded-lg bg-fd-primary text-fd-primary-foreground font-semibold text-lg hover:opacity-90 transition-opacity"
          >
            Get Started
          </Link>
          <a
            href="https://github.com/stackshy/cloudemu"
            target="_blank"
            rel="noopener noreferrer"
            className="px-6 py-3 rounded-lg border border-fd-border bg-fd-card font-semibold text-lg hover:bg-fd-accent transition-colors"
          >
            GitHub
          </a>
        </div>
      </motion.div>

      <motion.div
        initial={{ opacity: 0, y: 30 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.5, delay: 0.2 }}
        className="mt-14 max-w-xl mx-auto"
      >
        <div className="rounded-xl border border-fd-border bg-fd-card overflow-hidden shadow-lg">
          <div className="flex items-center gap-2 px-4 py-3 border-b border-fd-border bg-fd-secondary/50">
            <span className="w-3 h-3 rounded-full bg-red-500/70" />
            <span className="w-3 h-3 rounded-full bg-yellow-500/70" />
            <span className="w-3 h-3 rounded-full bg-green-500/70" />
            <span className="ml-2 text-xs text-fd-muted-foreground font-mono">main.go</span>
          </div>
          <pre className="p-6 text-left overflow-x-auto">
            <code className="text-sm font-mono leading-relaxed">
              <span className="text-fd-muted-foreground">{'// Create cloud providers — everything runs in memory'}</span>
              {'\n'}
              <span className="text-purple-400">aws</span>
              <span className="text-fd-muted-foreground">{' := '}</span>
              <span className="text-blue-400">cloudemu</span>
              <span className="text-fd-foreground">.NewAWS()</span>
              {'\n'}
              <span className="text-purple-400">azure</span>
              <span className="text-fd-muted-foreground">{' := '}</span>
              <span className="text-blue-400">cloudemu</span>
              <span className="text-fd-foreground">.NewAzure()</span>
              {'\n'}
              <span className="text-purple-400">gcp</span>
              <span className="text-fd-muted-foreground">{' := '}</span>
              <span className="text-blue-400">cloudemu</span>
              <span className="text-fd-foreground">.NewGCP()</span>
              {'\n\n'}
              <span className="text-fd-muted-foreground">{'// Use them exactly like real cloud SDKs'}</span>
              {'\n'}
              <span className="text-purple-400">instances</span>
              <span className="text-fd-muted-foreground">{', _ := '}</span>
              <span className="text-purple-400">aws</span>
              <span className="text-fd-foreground">.EC2.RunInstances(ctx, config, </span>
              <span className="text-orange-400">3</span>
              <span className="text-fd-foreground">)</span>
            </code>
          </pre>
        </div>
      </motion.div>
    </section>
  );
}
