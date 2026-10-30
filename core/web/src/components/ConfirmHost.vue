<script setup>
import { confirmState, answer } from '../lib/confirm.js';
import GlassWindow from './GlassWindow.vue';
</script>

<template>
  <GlassWindow v-if="confirmState.open" :title="confirmState.title" :icon="confirmState.danger ? 'alert' : 'shield'" :width="440" @close="answer(false)">
    <p class="muted">{{ confirmState.text }}</p>
    <template #foot>
      <button class="btn" :class="confirmState.danger ? 'danger' : 'primary'" @click="answer(true)">{{ $t(confirmState.ok) }}</button>
      <button class="btn" @click="answer(false)">{{ $t('انصراف') }}</button>
    </template>
  </GlassWindow>
</template>
