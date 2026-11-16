// overlayScroll hides the native horizontal scrollbar of a strip, such as
// the tab strip, and draws a thin thumb over its bottom edge instead. The
// thumb shows only while the strip scrolls and fades out after, so the strip
// keeps its height whether it overflows or not. A vertical wheel scrolls the
// strip sideways, and the thumb can be dragged.
//
// The parent of the node must be positioned (relative); the thumb is placed
// in it, over the node.
export function overlayScroll(node: HTMLElement) {
  node.classList.add('no-scrollbar');
  const parent = node.parentElement;
  if (!parent) return {};

  const thumb = document.createElement('div');
  thumb.className = 'overlay-thumb';
  parent.appendChild(thumb);

  let hideTimer: ReturnType<typeof setTimeout> | undefined;
  let dragging = false;

  function place() {
    const { scrollWidth, clientWidth, scrollLeft } = node;
    if (scrollWidth <= clientWidth + 1) {
      thumb.style.display = 'none';
      return false;
    }
    const width = Math.max(24, (clientWidth * clientWidth) / scrollWidth);
    const left = (scrollLeft / (scrollWidth - clientWidth)) * (clientWidth - width);
    thumb.style.display = '';
    thumb.style.width = `${width}px`;
    thumb.style.left = `${node.offsetLeft + left}px`;
    return true;
  }

  function show() {
    if (!place()) return;
    thumb.classList.add('visible');
    clearTimeout(hideTimer);
    hideTimer = setTimeout(function hide() {
      // A pointer resting on the thumb keeps it, so it can be grabbed.
      if (dragging || thumb.matches(':hover')) hideTimer = setTimeout(hide, 400);
      else thumb.classList.remove('visible');
    }, 800);
  }

  function onwheel(e: WheelEvent) {
    // A trackpad scrolls sideways by itself; a mouse wheel only goes up and
    // down, which means nothing for a strip that cannot scroll vertically.
    if (Math.abs(e.deltaX) >= Math.abs(e.deltaY) || node.scrollWidth <= node.clientWidth) return;
    e.preventDefault();
    node.scrollLeft += e.deltaY;
  }

  function ondown(e: PointerEvent) {
    e.preventDefault();
    e.stopPropagation();
    dragging = true;
    thumb.setPointerCapture(e.pointerId);
    const startX = e.clientX;
    const start = node.scrollLeft;
    const ratio = (node.scrollWidth - node.clientWidth) / Math.max(1, node.clientWidth - thumb.offsetWidth);
    const move = (ev: PointerEvent) => (node.scrollLeft = start + (ev.clientX - startX) * ratio);
    const up = () => {
      dragging = false;
      thumb.removeEventListener('pointermove', move);
      thumb.removeEventListener('pointerup', up);
      thumb.removeEventListener('pointercancel', up);
      show();
    };
    thumb.addEventListener('pointermove', move);
    thumb.addEventListener('pointerup', up);
    thumb.addEventListener('pointercancel', up);
  }

  // Tabs added or closed, or the window resized, change the thumb.
  const resize = new ResizeObserver(() => place());
  resize.observe(node);
  const mutate = new MutationObserver(() => place());
  mutate.observe(node, { childList: true });

  node.addEventListener('scroll', show, { passive: true });
  node.addEventListener('wheel', onwheel, { passive: false });
  thumb.addEventListener('pointerdown', ondown);
  place();

  return {
    destroy() {
      clearTimeout(hideTimer);
      resize.disconnect();
      mutate.disconnect();
      node.removeEventListener('scroll', show);
      node.removeEventListener('wheel', onwheel);
      thumb.remove();
    },
  };
}
