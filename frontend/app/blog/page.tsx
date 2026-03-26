import Link from 'next/link';

const posts = [
  {
    title: 'Introducing cloudemu',
    description: 'Zero-cost in-memory cloud emulation for Go',
    date: '2026-03-26',
    slug: 'hello-world',
  },
];

export default function BlogPage() {
  return (
    <main className="max-w-3xl mx-auto px-6 py-16">
      <h1 className="text-4xl font-bold mb-2">Blog</h1>
      <p className="text-fd-muted-foreground mb-10">
        Updates, tutorials, and insights from the cloudemu team
      </p>
      <div className="flex flex-col gap-6">
        {posts.map((post) => (
          <Link
            key={post.slug}
            href={`/blog/${post.slug}`}
            className="group block rounded-xl border border-fd-border bg-fd-card p-6 hover:border-fd-primary/50 hover:shadow-md transition-all"
          >
            <time className="text-sm text-fd-muted-foreground">{post.date}</time>
            <h2 className="text-xl font-semibold mt-1 group-hover:text-fd-primary transition-colors">
              {post.title}
            </h2>
            <p className="text-fd-muted-foreground mt-2">{post.description}</p>
          </Link>
        ))}
      </div>
    </main>
  );
}
