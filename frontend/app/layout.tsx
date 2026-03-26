import './global.css';
import { RootProvider } from 'fumadocs-ui/provider/next';
import type { ReactNode } from 'react';
import type { Metadata } from 'next';
import CustomSearchDialog from '@/components/search-dialog';

export const metadata: Metadata = {
  title: {
    template: '%s | cloudemu',
    default: 'cloudemu — Zero-Cost Cloud Emulation for Go',
  },
  description:
    'In-memory cloud service emulation for AWS, Azure, and GCP. No cloud accounts, no Docker, no network calls.',
  openGraph: {
    title: 'cloudemu — Zero-Cost Cloud Emulation for Go',
    description:
      'In-memory cloud service emulation for AWS, Azure, and GCP. No cloud accounts, no Docker, no network calls.',
    siteName: 'cloudemu',
  },
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body>
        <RootProvider
          theme={{
            attribute: 'class',
            defaultTheme: 'system',
            enableSystem: true,
          }}
          search={{
            SearchDialog: CustomSearchDialog,
          }}
        >
          {children}
        </RootProvider>
      </body>
    </html>
  );
}
