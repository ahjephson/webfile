<script setup lang="ts">
defineProps<{ text: string; incomplete?: boolean }>()

const warning = 'Some items could not be accessed and were excluded from the total.'
</script>

<template>
  <span>
    <span v-if="incomplete" v-tooltip.top="warning" tabindex="0" :title="warning" :aria-label="`${text}. ${warning}`" class="inline-flex items-center gap-1 text-yellow-500">
      {{ text }}<i class="pi pi-exclamation-triangle text-xs" aria-hidden="true" />
    </span>
    <template v-else>{{ text }}</template>
  </span>
</template>
