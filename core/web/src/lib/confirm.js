import { reactive } from 'vue';

// One confirmation at a time, answered through a promise.
export const confirmState = reactive({ open: false, title: '', text: '', ok: '', danger: false, resolve: null });

// The default button label is a Persian key; ConfirmHost shows it translated.
export function confirmBox(title, text, ok = 'تأیید', danger = false) {
  if (confirmState.resolve) confirmState.resolve(false);
  return new Promise((resolve) => {
    Object.assign(confirmState, { open: true, title, text, ok, danger, resolve });
  });
}

export function answer(v) {
  const r = confirmState.resolve;
  Object.assign(confirmState, { open: false, resolve: null });
  if (r) r(v);
}
