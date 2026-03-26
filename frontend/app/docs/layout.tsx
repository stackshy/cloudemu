import { DocsLayout } from 'fumadocs-ui/layouts/docs';
import type { ReactNode } from 'react';
import { source } from '@/lib/source';

export default function Layout({ children }: { children: ReactNode }) {
  return (
    <DocsLayout
      tree={source.pageTree}
      nav={{
        title: (
          <span className="font-bold text-lg">
            <span className="text-primary">cloud</span>emu
          </span>
        ),
        url: '/',
      }}
      sidebar={{
        defaultOpenLevel: 1,
      }}
      links={[
        {
          text: 'Blog',
          url: '/blog',
        },
      ]}
    >
      {children}
    </DocsLayout>
  );
}
