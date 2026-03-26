import { HomeLayout } from 'fumadocs-ui/layouts/home';
import type { ReactNode } from 'react';

export default function Layout({ children }: { children: ReactNode }) {
  return (
    <HomeLayout
      nav={{
        title: (
          <span className="font-bold text-lg">
            <span className="text-primary">cloud</span>emu
          </span>
        ),
        url: '/',
      }}
      links={[
        { text: 'Docs', url: '/docs' },
        { text: 'Blog', url: '/blog' },
        {
          text: 'GitHub',
          url: 'https://github.com/stackshy/cloudemu',
        },
      ]}
    >
      {children}
    </HomeLayout>
  );
}
