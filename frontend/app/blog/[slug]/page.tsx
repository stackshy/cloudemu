import { notFound } from 'next/navigation';
import fs from 'fs';
import path from 'path';
import type { Metadata } from 'next';

const blogDir = path.join(process.cwd(), 'content/blog');

async function getBlogPost(slug: string) {
  const filePath = path.join(blogDir, `${slug}.mdx`);
  if (!fs.existsSync(filePath)) return null;

  const source = fs.readFileSync(filePath, 'utf-8');
  const frontmatterMatch = source.match(/^---\n([\s\S]*?)\n---/);
  const content = source.replace(/^---\n[\s\S]*?\n---\n/, '');

  let title = slug;
  let description = '';
  if (frontmatterMatch) {
    const fm = frontmatterMatch[1];
    const titleMatch = fm.match(/title:\s*(.*)/);
    const descMatch = fm.match(/description:\s*(.*)/);
    if (titleMatch) title = titleMatch[1].trim();
    if (descMatch) description = descMatch[1].trim();
  }

  return { title, description, content };
}

export default async function BlogPostPage(props: {
  params: Promise<{ slug: string }>;
}) {
  const params = await props.params;
  const post = await getBlogPost(params.slug);
  if (!post) notFound();

  // Simple markdown-like rendering for blog posts
  const lines = post.content.split('\n');
  const html = lines
    .map((line) => {
      if (line.startsWith('# ')) return `<h1 class="text-4xl font-bold mt-8 mb-4">${line.slice(2)}</h1>`;
      if (line.startsWith('## ')) return `<h2 class="text-2xl font-bold mt-8 mb-3">${line.slice(3)}</h2>`;
      if (line.startsWith('### ')) return `<h3 class="text-xl font-semibold mt-6 mb-2">${line.slice(4)}</h3>`;
      if (line.startsWith('- ')) return `<li class="ml-6 list-disc text-fd-muted-foreground">${line.slice(2)}</li>`;
      if (line.startsWith('```')) return '';
      if (line.trim() === '') return '<br/>';
      return `<p class="text-fd-muted-foreground leading-relaxed">${line}</p>`;
    })
    .join('\n');

  return (
    <main className="max-w-3xl mx-auto px-6 py-16">
      <article>
        <h1 className="text-4xl font-bold mb-4">{post.title}</h1>
        <p className="text-fd-muted-foreground mb-8">{post.description}</p>
        <div
          className="prose dark:prose-invert max-w-none"
          dangerouslySetInnerHTML={{ __html: html }}
        />
      </article>
    </main>
  );
}

export async function generateMetadata(props: {
  params: Promise<{ slug: string }>;
}): Promise<Metadata> {
  const params = await props.params;
  const post = await getBlogPost(params.slug);
  if (!post) return {};
  return { title: post.title, description: post.description };
}
