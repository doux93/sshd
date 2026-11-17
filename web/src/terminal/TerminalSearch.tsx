import { useEffect, useRef } from "react";
import { Icon } from "../components/Icon";

export function TerminalSearch({
  query,
  onQuery,
  result,
  onFind,
  onClose,
}: {
  query: string;
  onQuery(value: string): void;
  result: { resultIndex: number; resultCount: number };
  onFind(previous: boolean): void;
  onClose(): void;
}) {
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => {
    input.current?.focus();
    input.current?.select();
  }, []);
  return (
    <section
      aria-label="搜索终端输出"
      className="terminal-search"
      onKeyDown={(event) => {
        if (event.nativeEvent.isComposing) return;
        if (event.key === "Escape") {
          event.preventDefault();
          event.stopPropagation();
          onClose();
        }
        if (event.key === "Enter") {
          event.preventDefault();
          onFind(event.shiftKey);
        }
      }}
    >
      <Icon name="search" size={16} />
      <input
        ref={input}
        aria-label="搜索内容"
        placeholder="搜索当前终端输出"
        value={query}
        onChange={(event) => onQuery(event.target.value)}
        className="min-w-0 flex-1 bg-transparent text-sm outline-none"
      />
      <span role="status" className="shrink-0 text-xs text-slate-400">
        {!query
          ? ""
          : !result.resultCount
            ? "无匹配"
            : result.resultIndex < 0
              ? `${result.resultCount}+ 项`
              : `${result.resultIndex + 1}/${result.resultCount}`}
      </span>
      <button
        className="icon-button"
        aria-label="上一个匹配"
        title="上一个（Shift+Enter）"
        disabled={!query}
        onClick={() => onFind(true)}
      >
        <Icon
          name="chevron"
          size={14}
          style={{ transform: "rotate(-90deg)" }}
        />
      </button>
      <button
        className="icon-button"
        aria-label="下一个匹配"
        title="下一个（Enter）"
        disabled={!query}
        onClick={() => onFind(false)}
      >
        <Icon name="chevron" size={14} style={{ transform: "rotate(90deg)" }} />
      </button>
      <button
        className="icon-button"
        aria-label="关闭终端搜索"
        title="关闭（Esc）"
        onClick={onClose}
      >
        <Icon name="close" size={14} />
      </button>
    </section>
  );
}
