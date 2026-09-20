<script lang="ts" generics="T">
  import type { Snippet } from 'svelte';

  interface Props {
    items: T[];
    row: Snippet<[T, number]>;
    rowHeight?: number;
    overscan?: number;
    label?: string;
  }

  let { items, row, rowHeight = 30, overscan = 10, label = '' }: Props = $props();

  let viewport: HTMLDivElement | undefined;
  let scrollTop = $state(0);
  let height = $state(600);

  const start = $derived(Math.max(0, Math.floor(scrollTop / rowHeight) - overscan));
  const end = $derived(Math.min(items.length, Math.ceil((scrollTop + height) / rowHeight) + overscan));
  const visible = $derived(items.slice(start, end));

  export function reset() {
    if (viewport) viewport.scrollTop = 0;
    scrollTop = 0;
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
