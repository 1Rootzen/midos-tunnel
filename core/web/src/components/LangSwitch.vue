<script setup>
import { i18n, setLang } from '../lib/i18n.js';

// Switches the panel between English and Persian; shows the language it
// switches to. The choice is remembered in this browser.
</script>

<template>
  <button type="button" class="btn small lang-switch" :title="i18n.lang === 'fa' ? 'English' : 'فارسی'"
    @click="setLang(i18n.lang === 'fa' ? 'en' : 'fa')">
    {{ i18n.lang === 'fa' ? 'EN' : 'فا' }}
  </button>
</template>
