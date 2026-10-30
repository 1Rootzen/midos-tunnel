import { createApp } from 'vue';
import App from './App.vue';
import router from './router.js';
import { t, apply } from './lib/i18n.js';
import './styles/main.css';

apply(); // set lang and dir before the first paint
const app = createApp(App);
app.config.globalProperties.$t = t;
app.use(router).mount('#app');
