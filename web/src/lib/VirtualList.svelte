<script lang="ts" generics="T">
  import type { Snippet } from 'svelte';

  interface Props {
    items: T[];
    row: Snippet<[T, number]>;
    rowHeight?: number;
    overscan?: number;
    label?: string;
    /** Called with the range of items actually on screen (without overscan). */
    onrange?: (start: number, end: number) => void;
  }

  let { items, row, rowHeight = 30, overscan = 10, label = '', onrange }: Props = $props();

  let viewport: HTMLDivElement | undefined;
  let scrollTop = $state(0);
  let height = $state(600);

  const start = $derived(Math.max(0, Math.floor(scrollTop / rowHeight) - overscan));
  const end = $derived(Math.min(items.length, Math.ceil((scrollTop + height) / rowHeight) + overscan));
  const visible = $derived(items.slice(start, end));

  $effect(() => {
    onrange?.(
      Math.max(0, Math.floor(scrollTop / rowHeight)),
      Math.min(items.length, Math.ceil((scrollTop + height) / rowHeight)),
    );
  });

  export function reset() {
    if (viewport) viewport.scrollTop = 0;
    scrollTop = 0;
  }

  /** Scrolls the minimum distance needed to bring row i fully into view. */
  export function scrollToIndex(i: number) {
    if (!viewport || i < 0) return;
    const top = i * rowHeight;
    if (top < viewport.scrollTop) viewport.scrollTop = top;
    else if (top + rowHeight > viewport.scrollTop + viewport.clientHeight) {
      viewport.scrollTop = top + rowHeight - viewport.clientHeight;
    }
    scrollTop = viewport.scrollTop;
  }
</script>

<div
  class="viewport"
  role="rowgroup"
  aria-label={label}
  bind:this={viewport}
  bind:clientHeight={height}
  onscroll={() => (scrollTop = viewport?.scrollTop ?? 0)}
>
  <div class="spacer" style:height="{items.length * rowHeight}px">
    {#each visible as item, i (start + i)}
      <div
        class="vrow"
        role="presentation"
        style:height="{rowHeight}px"
        style:transform="translateY({(start + i) * rowHeight}px)"
      >
        {@render row(item, start + i)}
      </div>
    {/each}
  </div>
</div>

<style>
  .viewport {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    overflow-x: hidden;
  }
  .spacer {
    position: relative;
  }
  .vrow {
    position: absolute;
    left: 0;
    right: 0;
    top: 0;
  }
</style>
