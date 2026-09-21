<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import Peek from './lib/Peek.svelte';
  import Preview from './lib/Preview.svelte';
  import VirtualList from './lib/VirtualList.svelte';
  import {
    ApiError,
    fileURL,
    getHead,
    getMeta,
    getStatus,
    streamList,
    streamSearch,
    type Head,
    type Row,
    type Status,
  } from './lib/api';
  import {
    COLUMN_IDS,
    COLUMNS,
    MAX_WIDTH,
    MIN_WIDTH,
    clampWidth,
    defaultLayout,
    gridTemplate,
    isHidden,
    isSortable,
    moveBy,
    moveColumn,
    parseLayout,
    toggleColumn,
    totalWidth,
    visibleOrder,
    type ColId,
    type Layout,
    type SortKey,
  } from './lib/columns';
  import {
    basename,
    crumbsFor,
    dirname,
    formatDate,
    formatSize,
    joinPath,
    kindOf,
    modeString,
    parseHash,
    pathToHash,
  } from './lib/format';
  import { keyOf } from './lib/keys';
  import { DEFAULT_PANE_WIDTH, MAX_PANE_WIDTH, MIN_PANE_WIDTH, clampPaneWidth, parsePrefs } from './lib/prefs';
  import { firstLines, plural } from './lib/preview';
  import { sortRows } from './lib/sort';

  const LAYOUT_KEY = 'fsb.columns.v1';
  const PREFS_KEY = 'fsb.prefs.v1';

  let status = $state<Status | null>(null);
  let path = $state('');
  let entries = $state.raw<Row[]>([]);
  let loading = $state(false);
  let error = $state('');
  let filter = $state('');
  let showHidden = $state(false);
  let sortKey = $state<SortKey>('name');
  let sortAsc = $state(true);
  let userSorted = $state(false);
  let filterInput: HTMLInputElement | undefined;
  let colMenu: HTMLDetailsElement | undefined;
  let searchEl: HTMLInputElement | undefined;
  let list: { reset(): void; scrollToIndex(i: number): void } | undefined;

  // ---- preferences kept in this browser (localStorage may be unavailable) ---
  function loadPrefs() {
    try {
      return parsePrefs(localStorage.getItem(PREFS_KEY), window.innerWidth);
    } catch {
      return parsePrefs(null, window.innerWidth);
    }
  }
  const initial = loadPrefs();
  let showPreview = $state(initial.preview);
  let hoverPeek = $state(initial.hover);
  let previewWidth = $state(initial.previewWidth);
  let foldersFirst = $state(initial.foldersFirst);

  function savePrefs() {
    try {
      localStorage.setItem(PREFS_KEY, JSON.stringify({ preview: showPreview, hover: hoverPeek, previewWidth, foldersFirst }));
    } catch {
      /* the choice just will not persist */
    }
  }

  // ---- preview pane width: drag its left edge, or use the keyboard ----------
  let paneDrag: { startX: number; startW: number } | null = null;

  function startPaneResize(ev: PointerEvent) {
    ev.preventDefault();
    (ev.currentTarget as HTMLElement).setPointerCapture(ev.pointerId);
    paneDrag = { startX: ev.clientX, startW: previewWidth };
  }

  function movePaneResize(ev: PointerEvent) {
    // The pane is on the right, so dragging left makes it wider.
    if (paneDrag) previewWidth = clampPaneWidth(paneDrag.startW + (paneDrag.startX - ev.clientX));
  }

  function endPaneResize() {
    if (!paneDrag) return;
    paneDrag = null;
    savePrefs();
  }

  function paneResizeKey(ev: KeyboardEvent) {
    const step = ev.shiftKey ? 50 : 10;
    const key = keyOf(ev);
    if (key === 'ArrowLeft' || key === 'ArrowRight') {
      ev.preventDefault();
      previewWidth = clampPaneWidth(previewWidth + (key === 'ArrowLeft' ? step : -step));
      savePrefs();
    }
  }

  function resetPaneWidth() {
    previewWidth = DEFAULT_PANE_WIDTH;
    savePrefs();
  }

  // ---- column layout ---------------------------------------------------------
  let layout = $state<Layout>(loadLayout());
  let resizing: { id: ColId; startX: number; startW: number } | null = null;
  let draggingCol = $state<ColId | null>(null);
  let dropTarget = $state<ColId | null>(null);
  const shownColumns = $derived(visibleOrder(layout));

  function loadLayout(): Layout {
    try {
      return parseLayout(localStorage.getItem(LAYOUT_KEY));
    } catch {
      return defaultLayout();
    }
  }

  function saveLayout() {
    try {
      localStorage.setItem(LAYOUT_KEY, JSON.stringify(layout));
    } catch {
      /* storage unavailable: the layout just won't persist */
    }
  }

  function setWidth(id: ColId, w: number) {
    layout = { ...layout, widths: { ...layout.widths, [id]: clampWidth(w) } };
  }

  function resetLayout() {
    layout = defaultLayout();
    try {
      localStorage.removeItem(LAYOUT_KEY);
    } catch {
      /* ignore */
    }
  }

  function toggleCol(id: ColId) {
    layout = toggleColumn(layout, id);
    saveLayout();
  }

  function startResize(ev: PointerEvent, id: ColId) {
    ev.preventDefault(); // also stops the header's native drag from starting
    (ev.currentTarget as HTMLElement).setPointerCapture(ev.pointerId);
    resizing = { id, startX: ev.clientX, startW: layout.widths[id] };
  }

  function moveResize(ev: PointerEvent) {
    if (resizing) setWidth(resizing.id, resizing.startW + ev.clientX - resizing.startX);
  }

  function endResize() {
    if (!resizing) return;
    resizing = null;
    saveLayout();
  }

  function resizeKey(ev: KeyboardEvent, id: ColId) {
    const step = ev.shiftKey ? 50 : 10;
    const key = keyOf(ev);
    if (key === 'ArrowLeft' || key === 'ArrowRight') {
      ev.preventDefault();
      setWidth(id, layout.widths[id] + (key === 'ArrowLeft' ? -step : step));
      saveLayout();
    } else if (key === 'Enter') {
      ev.preventDefault();
      fitColumn(id);
    }
  }

  let measureCtx: CanvasRenderingContext2D | null = null;

  /** Fits a column to the widest content among the rows currently rendered. */
  function fitColumn(id: ColId) {
    measureCtx ??= document.createElement('canvas').getContext('2d');
    const ctx = measureCtx;
    if (!ctx) return;
    const measure = (el: Element, text: string) => {
      ctx.font = getComputedStyle(el).font;
      return ctx.measureText(text).width;
    };
    let widest = 0;
    for (const cell of document.querySelectorAll<HTMLElement>(`.vrow [data-col="${id}"]`)) {
      const inner = cell.querySelector('.name') ?? cell; // folder names are bold
      widest = Math.max(widest, measure(inner, cell.textContent?.trim() ?? ''));
    }
    const head = document.querySelector<HTMLElement>(`.head [data-col="${id}"]`);
    if (head) widest = Math.max(widest, measure(head, head.textContent?.trim() ?? '') + 20);
    setWidth(id, Math.ceil(widest) + 8);
    saveLayout();
  }

  function dragStart(ev: DragEvent, id: ColId) {
    draggingCol = id;
    ev.dataTransfer?.setData('text/plain', id); // Firefox needs data to start a drag
    if (ev.dataTransfer) ev.dataTransfer.effectAllowed = 'move';
  }

  function dragOver(ev: DragEvent, id: ColId) {
    if (!draggingCol) return; // not one of our headers (e.g. a file dragged in)
    ev.preventDefault();
    dropTarget = id;
  }

  function drop(ev: DragEvent, id: ColId) {
    ev.preventDefault();
    if (draggingCol) {
      layout = { ...layout, order: moveColumn(layout.order, draggingCol, id) };
      saveLayout();
    }
    endDrag();
  }

  function endDrag() {
    draggingCol = null;
    dropTarget = null;
  }

  function headerKey(ev: KeyboardEvent, id: ColId) {
    const key = keyOf(ev);
    if (ev.altKey && (key === 'ArrowLeft' || key === 'ArrowRight')) {
      ev.preventDefault();
      layout = { ...layout, order: moveBy(layout, id, key === 'ArrowLeft' ? -1 : 1) };
      saveLayout();
    }
  }

  // ---- rows, sorting, selection ----------------------------------------------
  let searchActive = $state(false);
  let searching = $state(false);
  let searchInput = $state('');
  let searchRows = $state.raw<Row[]>([]);
  let searchNote = $state('');
  let searchCtrl: AbortController | undefined;
  let selectedKey = $state('');
  let pendingSelect = '';

  const rowPath = (e: Row) => e.path ?? joinPath(path, e.name);
  const source = $derived(searchActive ? searchRows : entries);
  const crumbs = $derived(status && path ? crumbsFor(path, status.roots) : []);

  const visible = $derived.by(() => {
    const q = filter.trim().toLowerCase();
    const rows = source.filter(
      (e) =>
        (showHidden || !e.name.startsWith('.')) &&
        (!q || (e.rel ?? e.name).toLowerCase().includes(q)),
    );
    // Search results arrive shallowest-first; keep that order until the user sorts.
    if (searchActive && !userSorted) return rows;
    return sortRows(rows, { key: sortKey, asc: sortAsc, foldersFirst });
  });

  const selectedIdx = $derived(selectedKey ? visible.findIndex((r) => rowPath(r) === selectedKey) : -1);
  const selectedRow = $derived(selectedIdx >= 0 ? visible[selectedIdx] : null);

  function describe(e: unknown): string {
    if (e instanceof ApiError) {
      if (e.status === 404) return 'Not found. The folder does not exist or is not available.';
      if (e.status === 403 && /permission/i.test(e.message)) {
        return 'Permission denied by the operating system or security software. On macOS, check System Settings > Privacy & Security, and any security tool that guards this folder.';
      }
      if (e.status === 403) return 'Session not authorized. Re-open the single-use URL that fsb printed when it started.';
      return `Server error (${e.status}).`;
    }
    return e instanceof Error ? e.message : String(e);
  }

  function sortBy(key: SortKey) {
    userSorted = true;
    if (sortKey === key) sortAsc = !sortAsc;
    else {
      sortKey = key;
      sortAsc = true;
    }
  }

  function ariaSort(key: SortKey): 'ascending' | 'descending' | 'none' {
    return sortKey !== key || (searchActive && !userSorted) ? 'none' : sortAsc ? 'ascending' : 'descending';
  }

  function select(e: Row) {
    selectedKey = rowPath(e);
  }

  function revealSelected() {
    tick().then(() => {
      if (selectedIdx >= 0) list?.scrollToIndex(selectedIdx);
    });
  }

  function selectByName(name: string) {
    clearSearch();
    if (name.startsWith('.')) showHidden = true;
    selectedKey = joinPath(path, name);
    revealSelected();
  }

  function move(delta: number) {
    if (visible.length === 0) return;
    const i =
      selectedIdx < 0
        ? delta > 0
          ? 0
          : visible.length - 1
        : Math.min(visible.length - 1, Math.max(0, selectedIdx + delta));
    selectedKey = rowPath(visible[i]);
    lastNavAt = Date.now();
    list?.scrollToIndex(i);
  }

  function open(e: Row) {
    if (e.isDir && !e.broken) location.hash = pathToHash(rowPath(e));
    else if (searchActive) location.hash = pathToHash(dirname(rowPath(e)), e.name);
    else {
      showPreview = true;
      savePrefs();
    }
  }

  function goUp() {
    if (searchActive) {
      clearSearch();
      return;
    }
    if (crumbs.length > 1) location.hash = pathToHash(crumbs[crumbs.length - 2].path, basename(path));
  }

  // ---- copy path -------------------------------------------------------------
  let toast = $state('');
  let toastTimer: ReturnType<typeof setTimeout> | undefined;

  async function copyText(text: string) {
    let ok = false;
    try {
      await navigator.clipboard.writeText(text);
      ok = true;
    } catch {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      ta.className = 'sr-copy';
      document.body.appendChild(ta);
      ta.select();
      try {
        ok = document.execCommand('copy');
      } catch {
        ok = false;
      }
      ta.remove();
    }
    toast = ok ? 'Copied path to the clipboard' : 'Could not copy';
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => (toast = ''), 1800);
  }

  // ---- search ----------------------------------------------------------------
  function clearSearch() {
    searchCtrl?.abort();
    searchCtrl = undefined;
    searchActive = false;
    searching = false;
    searchRows = [];
    searchNote = '';
    searchInput = '';
    userSorted = false;
  }

  function runSearch() {
    const query = searchInput.trim();
    if (!query) {
      clearSearch();
      return;
    }
    searchCtrl?.abort();
    const ctrl = new AbortController();
    searchCtrl = ctrl;
    searchActive = true;
    searching = true;
    searchRows = [];
    searchNote = '';
    selectedKey = '';
    userSorted = false;
    error = '';
    list?.reset();

    let buf: Row[] = [];
    let acc: Row[] = [];
    let timer: ReturnType<typeof setTimeout> | undefined;
    const flush = () => {
      timer = undefined;
      if (buf.length) {
        acc = acc.concat(buf);
        buf = [];
        searchRows = acc;
      }
    };
    streamSearch(
      path,
      query,
      (rows) => {
        buf.push(...rows);
        if (acc.length === 0) flush();
        else timer ??= setTimeout(flush, 120);
      },
      ctrl.signal,
    )
      .then((done) => {
        clearTimeout(timer);
        flush();
        searching = false;
        if (done.truncated) searchNote = 'Stopped early: too many results or too many items to scan. Narrow the search.';
      })
      .catch((e) => {
        if (ctrl.signal.aborted) return;
        clearTimeout(timer);
        flush();
        searching = false;
        error = describe(e);
      });
  }

  // ---- hover peek ------------------------------------------------------------
  let peek = $state<{ x: number; y: number; text: string; cut: boolean; note: string } | null>(null);
  let peekTimer: ReturnType<typeof setTimeout> | undefined;
  let peekCtrl: AbortController | undefined;
  let lastNavAt = 0;
  let lastScrollAt = 0;
  const peekCache = new Map<string, Head>();
  const PEEK_DELAY = 400;

  function clearPeek() {
    clearTimeout(peekTimer);
    peekCtrl?.abort();
    peekCtrl = undefined;
    peek = null;
  }

  function peekEnter(ev: MouseEvent, e: Row) {
    clearPeek();
    if (!hoverPeek || e.isDir || e.broken || e.size === 0) return;
    // Not while the keyboard or a scroll is moving rows under a resting mouse.
    if (Date.now() - lastNavAt < 800 || Date.now() - lastScrollAt < 400) return;
    const p = rowPath(e);
    const key = `${p}|${e.modTime}|${e.size}`;
    const { clientX: x, clientY: y } = ev;
    peekTimer = setTimeout(async () => {
      let h = peekCache.get(key);
      if (!h) {
        const ctrl = new AbortController();
        peekCtrl = ctrl;
        try {
          h = await getHead(p, 2048, ctrl.signal);
        } catch {
          return; // denied, vanished, unreadable: no bubble
        }
        if (ctrl.signal.aborted) return;
        if (peekCache.size > 200) peekCache.clear();
        peekCache.set(key, h);
      }
      if (h.kind === 'text' && h.text) {
        const f = firstLines(h.text, 20, 1200);
        peek = { x, y, text: f.text, cut: f.cut || !!h.truncated, note: '' };
      } else if (h.kind === 'dataless') {
        peek = { x, y, text: '', cut: false, note: 'Stored in the cloud and not downloaded.' };
      }
    }, PEEK_DELAY);
  }

  // ---- extended-attributes column: fetched lazily for the rows on screen -----
  let range = $state({ start: 0, end: 0 });
  let xattrVersion = $state(0);
  const xattrCache = new Map<string, string>();
  const xkey = (e: Row) => `${rowPath(e)}|${e.modTime}`;

  function xattrText(e: Row): string {
    void xattrVersion; // re-render when more names arrive
    if (e.broken) return '';
    const v = xattrCache.get(xkey(e));
    return v === undefined ? '…' : v;
  }

  $effect(() => {
    if (isHidden(layout, 'xattr')) return;
    const wanted = visible.slice(range.start, range.end).filter((e) => !e.broken && !xattrCache.has(xkey(e)));
    if (wanted.length === 0) return;
    const ctrl = new AbortController();
    const timer = setTimeout(async () => {
      let next = 0;
      const worker = async () => {
        while (next < wanted.length && !ctrl.signal.aborted) {
          const e = wanted[next++];
          try {
            const m = await getMeta(rowPath(e), false, ctrl.signal);
            xattrCache.set(xkey(e), m.xattrs.map((x) => x.name).join(', '));
          } catch {
            if (ctrl.signal.aborted) return;
            xattrCache.set(xkey(e), '');
          }
        }
      };
      await Promise.all([worker(), worker(), worker(), worker()]);
      if (!ctrl.signal.aborted) xattrVersion++;
    }, 150);
    return () => {
      clearTimeout(timer);
      ctrl.abort();
    };
  });

  // ---- keyboard --------------------------------------------------------------
  function onKey(ev: KeyboardEvent) {
    lastNavAt = Date.now();
    clearPeek();
    if (ev.key === 'Escape' && colMenu?.open) {
      colMenu.open = false;
      return;
    }
    if (ev.metaKey || ev.ctrlKey || ev.altKey) return;
    const t = ev.target as HTMLElement | null;
    const typing = t instanceof HTMLInputElement || t instanceof HTMLTextAreaElement || t instanceof HTMLSelectElement;
    if (typing) {
      if (ev.key === 'Escape') {
        if (t === filterInput) filter = '';
        if (t === searchEl) clearSearch();
        (t as HTMLElement).blur();
      } else if (ev.key === 'Enter' && t === searchEl) {
        runSearch();
      } else if (ev.key === 'ArrowDown' && (t === filterInput || t === searchEl)) {
        ev.preventDefault();
        (t as HTMLElement).blur();
        move(1);
      }
      return;
    }
    const onControl = t instanceof HTMLAnchorElement || t instanceof HTMLButtonElement || t?.tagName === 'SUMMARY';
    const page = 10;
    const key = keyOf(ev);
    switch (key) {
      case '/':
        ev.preventDefault();
        filterInput?.focus();
        break;
      case 's':
        ev.preventDefault();
        searchEl?.focus();
        break;
      case 'ArrowDown':
        ev.preventDefault();
        move(1);
        break;
      case 'ArrowUp':
        ev.preventDefault();
        move(-1);
        break;
      case 'PageDown':
        ev.preventDefault();
        move(page);
        break;
      case 'PageUp':
        ev.preventDefault();
        move(-page);
        break;
      case 'Home':
        ev.preventDefault();
        if (visible.length) {
          selectedKey = rowPath(visible[0]);
          list?.scrollToIndex(0);
        }
        break;
      case 'End':
        ev.preventDefault();
        if (visible.length) {
          selectedKey = rowPath(visible[visible.length - 1]);
          list?.scrollToIndex(visible.length - 1);
        }
        break;
      case 'ArrowRight':
        if (selectedRow?.isDir) {
          ev.preventDefault();
          open(selectedRow);
        }
        break;
      case 'ArrowLeft':
      case 'Backspace':
        ev.preventDefault();
        goUp();
        break;
      case 'Enter':
        if (!onControl && selectedRow) {
          ev.preventDefault();
          open(selectedRow);
        }
        break;
      case ' ':
        if (!onControl) {
          ev.preventDefault();
          showPreview = !showPreview;
          savePrefs();
        }
        break;
      case 'c':
        copyText(selectedRow ? rowPath(selectedRow) : path);
        break;
      case 'Escape':
        if (searchActive) clearSearch();
        else selectedKey = '';
        break;
    }
  }

  // ---- data loading ----------------------------------------------------------
  onMount(() => {
    const onHash = () => {
      const { path: p, select: sel } = parseHash(location.hash);
      if (!p) return;
      if (p === path) {
        if (sel) selectByName(sel);
        return;
      }
      pendingSelect = sel;
      path = p;
    };
    const onScroll = () => {
      lastScrollAt = Date.now();
      clearPeek();
    };
    const onOutside = (ev: PointerEvent) => {
      if (colMenu?.open && !colMenu.contains(ev.target as Node)) colMenu.open = false;
    };
    window.addEventListener('hashchange', onHash);
    window.addEventListener('keydown', onKey);
    window.addEventListener('pointerdown', onOutside, true);
    window.addEventListener('scroll', onScroll, true);
    window.addEventListener('wheel', onScroll, { passive: true });
    getStatus()
      .then((s) => {
        status = s;
        if (!parseHash(location.hash).path && s.roots.length) {
          history.replaceState(null, '', pathToHash(s.roots[0]));
        }
        onHash();
      })
      .catch((e) => (error = describe(e)));
    return () => {
      window.removeEventListener('hashchange', onHash);
      window.removeEventListener('keydown', onKey);
      window.removeEventListener('pointerdown', onOutside, true);
      window.removeEventListener('scroll', onScroll, true);
      window.removeEventListener('wheel', onScroll);
    };
  });

  // Load (stream) the directory whenever the path changes.
  $effect(() => {
    const p = path;
    if (!p) return;
    const ctrl = new AbortController();
    entries = [];
    error = '';
    loading = true;
    filter = '';
    untrack(() => {
      list?.reset();
      clearSearch();
      clearPeek();
      xattrCache.clear();
      selectedKey = '';
    });

    // Throttle state updates so a 100k-entry directory does not re-sort on
    // every chunk.
    let buf: Row[] = [];
    let acc: Row[] = [];
    let timer: ReturnType<typeof setTimeout> | undefined;
    const flush = () => {
      timer = undefined;
      if (buf.length) {
        acc = acc.concat(buf);
        buf = [];
        entries = acc;
      }
    };
    const applyPending = () => {
      if (pendingSelect) {
        const name = pendingSelect;
        pendingSelect = '';
        selectByName(name);
      }
    };

    streamList(
      p,
      (es) => {
        buf.push(...es);
        if (acc.length === 0) flush(); // paint the first chunk immediately
        else timer ??= setTimeout(flush, 120);
      },
      ctrl.signal,
    )
      .then(() => {
        clearTimeout(timer);
        flush();
        loading = false;
        applyPending();
      })
      .catch((e) => {
        if (ctrl.signal.aborted) return;
        clearTimeout(timer);
        flush();
        loading = false;
        error = describe(e);
      });

    return () => {
      ctrl.abort();
      clearTimeout(timer);
    };
  });

  $effect(() => {
    document.title = path ? `${path.split('/').filter(Boolean).pop() ?? '/'} - fsb` : 'fsb';
  });
</script>

{#snippet cell(id: ColId, e: Row)}
  {#if id === 'name'}
    <div role="gridcell" class="c-name" data-col="name">
      {#if e.broken}
        <span class="name broken" title="Broken symlink">{e.rel ?? e.name}</span>
      {:else if e.isDir}
        <a class="name dir" href={pathToHash(rowPath(e))}>{e.rel ?? e.name}/</a>
      {:else if searchActive}
        <a class="name" href={pathToHash(dirname(rowPath(e)), e.name)} title="Show in its folder">{e.rel ?? e.name}</a>
      {:else}
        <a class="name" href={fileURL(rowPath(e))} download={e.name}>{e.name}</a>
      {/if}
      {#if e.isSymlink}<span class="link" title="Symbolic link">→</span>{/if}
    </div>
  {:else if id === 'size'}
    <div role="gridcell" class="c-size" data-col="size">{e.isDir ? '' : formatSize(e.size)}</div>
  {:else if id === 'modTime'}
    <div role="gridcell" class="c-date" data-col="modTime">{formatDate(e.modTime)}</div>
  {:else if id === 'kind'}
    <div role="gridcell" class="c-kind" data-col="kind">{kindOf(e)}</div>
  {:else if id === 'mode'}
    <div role="gridcell" class="c-mode mono" data-col="mode">{modeString(e)}</div>
  {:else}
    <div role="gridcell" class="c-xattr mono" data-col="xattr" title={xattrText(e)}>{xattrText(e)}</div>
  {/if}
{/snippet}

{#if status && status.coreDenyMissing.length > 0}
  <div class="banner" role="alert">
    <strong>Warning:</strong> core deny rule(s) disabled: <code>{status.coreDenyMissing.join(', ')}</code>. These paths
    are browsable. Restore them in <code>~/.config/fsb/deny</code> (see the README section on deny rules).
  </div>
{/if}

<header class="bar">
  <nav class="crumbs" aria-label="Path">
    {#each crumbs as c, i (c.path)}
      {#if i > 0}<span class="sep" aria-hidden="true">/</span>{/if}
      {#if i === crumbs.length - 1 && !searchActive}
        <span class="crumb current" aria-current="page">{c.label}</span>
      {:else}
        <!-- While search results are shown, the current folder's crumb is a way back to its listing. -->
        <a
          class="crumb"
          class:current={i === crumbs.length - 1}
          href={pathToHash(c.path)}
          title={searchActive && i === crumbs.length - 1 ? 'Leave the search results' : undefined}
          onclick={(ev) => {
            if (searchActive && i === crumbs.length - 1) {
              ev.preventDefault();
              clearSearch();
            }
          }}>{c.label}</a
        >
      {/if}
    {/each}
    {#if path}
      <button class="textbtn small" onclick={() => copyText(path)} title="Copy this folder's path">Copy path</button>
    {/if}
  </nav>
  <span class="badge" title="fsb never writes to your filesystem">read-only</span>
</header>

<div class="tools">
  <input
    bind:this={filterInput}
    bind:value={filter}
    type="search"
    placeholder="Filter this folder   ( / )"
    aria-label="Filter this folder"
    autocomplete="off"
    spellcheck="false"
  />
  <input
    bind:this={searchEl}
    bind:value={searchInput}
    type="search"
    placeholder="Search subfolders by name, then Enter   ( s )"
    aria-label="Search subfolders by name"
    autocomplete="off"
    spellcheck="false"
  />
  <label class="toggle"><input type="checkbox" bind:checked={showHidden} /> Hidden files</label>
  <label class="toggle" title="List folders before files, whichever column you sort by">
    <input type="checkbox" bind:checked={foldersFirst} onchange={savePrefs} /> Folders first
  </label>
  <label class="toggle" title="Show the first lines of a file when you hover over it">
    <input type="checkbox" bind:checked={hoverPeek} onchange={() => { savePrefs(); clearPeek(); }} /> Hover previews
  </label>
  <label class="toggle" title="Show or hide the preview pane (Space)">
    <input type="checkbox" bind:checked={showPreview} onchange={savePrefs} /> Preview pane
  </label>
  <details class="colmenu" bind:this={colMenu}>
    <summary>Columns</summary>
    <div class="menu">
      {#each COLUMN_IDS as id (id)}
        <label>
          <input type="checkbox" checked={!isHidden(layout, id)} disabled={id === 'name'} onchange={() => toggleCol(id)} />
          {COLUMNS[id].label}
        </label>
      {/each}
      <button class="textbtn" onclick={resetLayout}>Reset columns</button>
    </div>
  </details>
</div>

<div class="main" style:--pw="{previewWidth}px">
  <div class="table">
    <div
      class="grid"
      role="grid"
      tabindex="0"
      aria-label="Files"
      aria-rowcount={visible.length}
      aria-activedescendant={selectedIdx >= 0 ? `row-${selectedIdx}` : undefined}
      style:--cols={gridTemplate(layout)}
      style:--total="{totalWidth(layout)}px"
    >
      <div class="row head" role="row">
        {#each shownColumns as id (id)}
          <div
            role="columnheader"
            class="hcell {COLUMNS[id].cls}"
            data-col={id}
            aria-sort={isSortable(id) ? ariaSort(id) : undefined}
          >
            <div
              class="hdr"
              class:drop={dropTarget === id && draggingCol !== id}
              class:dragging={draggingCol === id}
              role="presentation"
              draggable="true"
              ondragstart={(ev) => dragStart(ev, id)}
              ondragover={(ev) => dragOver(ev, id)}
              ondrop={(ev) => drop(ev, id)}
              ondragend={endDrag}
            >
              <button
                onclick={() => isSortable(id) && sortBy(id)}
                onkeydown={(ev) => headerKey(ev, id)}
                title={isSortable(id) ? 'Click to sort. Drag or Alt+Left/Right to move.' : 'Drag or Alt+Left/Right to move.'}
              >
                {COLUMNS[id].label}{isSortable(id) && sortKey === id && (!searchActive || userSorted) ? (sortAsc ? ' ▲' : ' ▼') : ''}
              </button>
            </div>
            <!-- A focusable separator with aria-valuenow is the ARIA "window splitter" widget; the lint rules treat it as static. -->
            <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
            <div
              class="resize"
              role="separator"
              aria-orientation="vertical"
              aria-label="Resize {COLUMNS[id].label} column"
              aria-valuenow={layout.widths[id]}
              aria-valuemin={MIN_WIDTH}
              aria-valuemax={MAX_WIDTH}
              tabindex="0"
              onpointerdown={(ev) => startResize(ev, id)}
              onpointermove={moveResize}
              onpointerup={endResize}
              onpointercancel={endResize}
              onkeydown={(ev) => resizeKey(ev, id)}
              ondblclick={() => fitColumn(id)}
            ></div>
          </div>
        {/each}
      </div>

      {#if error}
        <p class="msg error" role="alert">{error}</p>
      {:else if !loading && !searching && visible.length === 0}
        <p class="msg">
          {searchActive ? 'No matches.' : source.length === 0 ? 'This folder is empty.' : 'No items match.'}
        </p>
      {/if}

      <VirtualList
        bind:this={list}
        items={visible}
        label="Directory entries"
        onrange={(start, end) => {
          if (start !== range.start || end !== range.end) range = { start, end };
        }}
      >
        {#snippet row(e: Row, idx: number)}
          <!-- Keyboard use is handled globally (arrow keys, Enter, Space); the click only selects. -->
          <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
          <div
            class="row"
            class:selected={rowPath(e) === selectedKey}
            role="row"
            tabindex="-1"
            id="row-{idx}"
            aria-selected={rowPath(e) === selectedKey}
            onclick={() => select(e)}
            onmouseenter={(ev) => peekEnter(ev, e)}
            onmouseleave={clearPeek}
          >
            {#each shownColumns as id (id)}
              {@render cell(id, e)}
            {/each}
          </div>
        {/snippet}
      </VirtualList>
    </div>
  </div>

  {#if showPreview}
    <!-- A focusable separator with aria-valuenow is the ARIA "window splitter" widget; the lint rules treat it as static. -->
    <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
    <div
      class="psplit"
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize preview pane"
      aria-valuenow={previewWidth}
      aria-valuemin={MIN_PANE_WIDTH}
      aria-valuemax={MAX_PANE_WIDTH}
      title="Drag to resize; double-click to reset. Keyboard: Left/Right"
      tabindex="0"
      onpointerdown={startPaneResize}
      onpointermove={movePaneResize}
      onpointerup={endPaneResize}
      onpointercancel={endPaneResize}
      onkeydown={paneResizeKey}
      ondblclick={resetPaneWidth}
    ></div>
    <Preview
      entry={selectedRow}
      fullPath={selectedRow ? rowPath(selectedRow) : ''}
      onclose={() => { showPreview = false; savePrefs(); }}
      oncopy={copyText}
    />
  {/if}
</div>

{#if peek}
  <Peek x={peek.x} y={peek.y} text={peek.text} cut={peek.cut} note={peek.note} />
{/if}

{#if toast}
  <div class="toast" role="status">{toast}</div>
{/if}

<footer class="status" aria-live="polite">
  {#if searchActive}
    {searching ? 'Searching…' : 'Search done:'}
    {plural(source.length, 'match', 'matches')} under this folder{searchNote ? ` — ${searchNote}` : ''}
    <button class="textbtn small" onclick={clearSearch}>Clear search</button>
  {:else if loading}
    Loading… {entries.length.toLocaleString()} items so far
  {:else}
    {visible.length.toLocaleString()} of {entries.length.toLocaleString()} items
  {/if}
  <span class="keys">↑↓ select · Enter/→ open · ←/Backspace up · Space preview · / filter · s search · c copy path</span>
</footer>
