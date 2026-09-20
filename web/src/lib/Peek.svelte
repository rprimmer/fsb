<script lang="ts">
  // The hover bubble: the first lines of a file, as plain text only. The text
  // is bound with {text}, never as HTML.
  interface Props {
    x: number;
    y: number;
    text: string;
    cut: boolean;
    note: string;
  }

  let { x, y, text, cut, note }: Props = $props();

  const WIDTH = 380;
  const HEIGHT = 250;
  const left = $derived(Math.max(8, Math.min(x + 16, innerWidth - WIDTH - 8)));
  const top = $derived(y + 22 + HEIGHT > innerHeight ? Math.max(8, y - HEIGHT - 10) : y + 22);
</script>

<div class="peek" role="tooltip" style:left="{left}px" style:top="{top}px">
  {#if note}
    <p class="note">{note}</p>
  {:else}
    <pre>{text}</pre>
    {#if cut}<p class="more">…</p>{/if}
  {/if}
</div>

<style>
  .peek {
    position: fixed;
    z-index: 50;
    width: 380px;
    max-height: 250px;
    overflow: hidden;
    padding: 8px 10px;
    background: var(--bg);
    color: var(--fg);
    border: 1px solid var(--line);
    border-radius: 8px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.2);
    pointer-events: none;
  }
  pre {
    margin: 0;
    font: 12px/1.4 ui-monospace, SFMono-Regular, Menlo, monospace;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .note {
    margin: 0;
    color: var(--muted);
  }
  .more {
    margin: 2px 0 0;
    color: var(--muted);
  }
</style>
