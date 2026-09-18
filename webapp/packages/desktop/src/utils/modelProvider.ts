import { modelProviderIcons } from './modelProviderIcons'

// 顺序及关键词与 rc35 ModelBadge 的 resolveModelProvider 保持一致，未知模型不伪装成品牌。
const providers: readonly [string, readonly string[]][] = [
  ['OpenAI', ['gpt-', 'chatgpt-', 'text-embedding-', 'omni-moderation', 'dall-e', 'whisper', 'tts-']],
  ['Claude', ['claude-', 'anthropic']], ['Gemini', ['gemini-', 'learnlm-']],
  ['Grok', ['grok-', 'xai-']], ['DeepSeek', ['deepseek-']], ['Qwen', ['qwen', 'qwq-']],
  ['Doubao', ['doubao-', 'volcengine']], ['Moonshot', ['moonshot-', 'kimi-']],
  ['Minimax', ['minimax', 'abab']], ['Zhipu', ['glm-', 'chatglm', 'cogview', 'cogvideo']],
  ['XiaomiMiMo', ['mimo-']], ['Wenxin', ['ernie']], ['Spark', ['spark']],
  ['Hunyuan', ['hunyuan']], ['Baichuan', ['baichuan']], ['InternLM', ['internlm']],
  ['Stepfun', ['step-']], ['Yi', ['yi-']], ['Mistral', ['mistral-', 'mixtral-']],
  ['Meta', ['llama-', 'meta-']], ['Cohere', ['command-', 'cohere-']],
]

/** 返回本地品牌图标资源，不发起 CDN 请求；OpenAI 推理模型遵循 rc35 的独立匹配规则。 */
export function resolveModelProvider(name: string): { name: string; src: string } | undefined {
  const model = name.toLowerCase()
  const provider = providers.find(([brand, keywords]) =>
    keywords.some(keyword => model.includes(keyword)) || (brand === 'OpenAI' && /\bo[134](?:-|$)/.test(model)),
  )?.[0]
  return provider ? { name: provider, src: modelProviderIcons[provider] } : undefined
}
