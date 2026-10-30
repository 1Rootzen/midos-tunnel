<script setup>
import { useRouter } from 'vue-router';
import PageHead from '../components/PageHead.vue';
import TunnelForm from '../components/TunnelForm.vue';

const props = defineProps({ id: { type: String, required: true } });
const router = useRouter();
const back = () => router.push('/tunnel/' + encodeURIComponent(props.id));
</script>

<template>
  <div>
    <PageHead :title="$t('ویرایش {id}', { id })" tag="EDIT // RESTART ON SAVE" />
    <TunnelForm :id="id" @done="back" @cancel="back" />
  </div>
</template>
