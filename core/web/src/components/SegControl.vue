<script setup>
defineProps({ options: { type: Array, required: true }, modelValue: { type: [String, Boolean, Number], default: '' } });
const emit = defineEmits(['update:modelValue']);
</script>

<template>
  <div class="seg" role="radiogroup">
    <button v-for="[v, label] in options" :key="String(v)" type="button" role="radio"
      :aria-checked="v === modelValue" :class="{ on: v === modelValue }" @click="emit('update:modelValue', v)">{{ label }}</button>
  </div>
</template>
