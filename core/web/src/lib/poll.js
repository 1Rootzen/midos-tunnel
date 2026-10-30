import { onMounted, onBeforeUnmount } from 'vue';
import { fail } from './toast.js';

// usePoll runs fn now and then every ms while the page is mounted. It pauses
// while the browser tab is hidden (no point refreshing what nobody sees) and
// catches up the moment the tab is shown again.
export function usePoll(fn, ms) {
  let timer = 0;
  let alive = true;
  let running = false;

  const schedule = () => {
    clearTimeout(timer);
    if (alive) timer = setTimeout(tick, ms);
  };
  async function tick() {
    if (!alive) return;
    if (document.hidden || running) { schedule(); return; }
    running = true;
    try { await fn(); } catch (e) { fail(e); }
    running = false;
    schedule();
  }
  const onVisible = () => {
    if (!document.hidden && alive) { clearTimeout(timer); tick(); }
  };

  onMounted(() => {
    tick();
    document.addEventListener('visibilitychange', onVisible);
  });
  onBeforeUnmount(() => {
    alive = false;
    clearTimeout(timer);
    document.removeEventListener('visibilitychange', onVisible);
  });
  return { refresh: () => { clearTimeout(timer); return tick(); } };
}
