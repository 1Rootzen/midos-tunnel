import { createRouter, createWebHashHistory } from 'vue-router';
import DashboardPage from './pages/DashboardPage.vue';
import { store } from './lib/store.js';

// The dashboard ships in the main bundle; every other page loads on first
// visit, so the login and first screen arrive as fast as possible.
const routes = [
  { path: '/', redirect: '/dashboard' },
  { path: '/dashboard', component: DashboardPage, meta: { nav: 'dashboard' } },
  { path: '/tunnels', component: () => import('./pages/TunnelsPage.vue'), meta: { nav: 'tunnels' } },
  { path: '/tunnel/:id', component: () => import('./pages/TunnelPage.vue'), props: true, meta: { nav: 'tunnels' } },
  { path: '/edit/:id', component: () => import('./pages/EditPage.vue'), props: true, meta: { nav: 'tunnels' } },
  { path: '/kernel', component: () => import('./pages/KernelPage.vue'), meta: { nav: 'tunnels' } },
  { path: '/servers', component: () => import('./pages/ServersPage.vue'), meta: { nav: 'servers' } },
  { path: '/topology', component: () => import('./pages/TopologyPage.vue'), meta: { nav: 'topology' } },
  { path: '/benchmark', component: () => import('./pages/BenchmarkPage.vue'), meta: { nav: 'benchmark' } },
  { path: '/usage', component: () => import('./pages/UsagePage.vue'), meta: { nav: 'usage' } },
  { path: '/doctor', component: () => import('./pages/DoctorPage.vue'), meta: { nav: 'doctor' } },
  { path: '/settings', component: () => import('./pages/SettingsPage.vue'), meta: { nav: 'settings' } },
  { path: '/audit', component: () => import('./pages/AuditPage.vue'), meta: { nav: 'audit' } },
  // an old #/new link opens the create window over the dashboard
  { path: '/new', redirect: () => { store.newTunnel = true; return '/dashboard'; } },
  { path: '/:rest(.*)*', redirect: '/dashboard' },
];

export default createRouter({
  history: createWebHashHistory(),
  routes,
  scrollBehavior: () => ({ top: 0 }),
});
