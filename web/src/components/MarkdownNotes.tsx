import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";

export function MarkdownNotes({ text }: { text: string }) {
  return (
    <div className="markdown-notes">
      <Markdown
        remarkPlugins={[remarkGfm]}
        skipHtml
        components={{
          a: ({ children, href }) => href ? (
            <a href={href} target="_blank" rel="noopener noreferrer">{children}</a>
          ) : <span>{children}</span>,
          img: ({ alt }) => alt ? <span>{alt}</span> : null,
        }}
      >{text}</Markdown>
    </div>
  );
}
