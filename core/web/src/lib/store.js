import { reactive } from 'vue';

// App-wide state: who is logged in, and which global windows are open.
export const store = reactive({
  booted: false,
  session: null,
  newTunnel: false, // the create-tunnel window
  newTunnelKind: '', // '' or 'kernel': which kind the window opens on
});
