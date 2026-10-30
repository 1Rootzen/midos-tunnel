<script setup>
import { computed } from 'vue';
import { ICONS } from '../lib/icons.js';

const props = defineProps({
  name: { type: String, required: true },
  spin: Boolean,
  draw: Boolean, // play the draw-on animation once when shown
});
const markup = computed(() => ICONS[props.name] || '');
</script>

<template>
  <svg class="ic" :class="['ic-' + name, { spin, draw }]" viewBox="0 0 24 24" fill="none" stroke="currentColor"
    stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" v-html="markup"></svg>
</template>
