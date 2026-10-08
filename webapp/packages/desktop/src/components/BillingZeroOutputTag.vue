<script setup lang="ts">
import {computed} from 'vue';
import type {BillingJob} from '@ct/shared';
const props=defineProps<{job:BillingJob}>();
const policy=computed(()=>props.job.zero_output_policy || (props.job.bill_period==='monthly'?'unknown':props.job.exclude_zero_output?'excluded':'included'));
const labels={included:'包含零输出',excluded:'排除零输出',mixed:'部分包含零输出',unknown:'零输出规则未记录'};
</script>
<template><el-tag size="small" effect="plain" :type="policy==='excluded'?'warning':policy==='included'?'info':'danger'">{{labels[policy]}}</el-tag></template>
