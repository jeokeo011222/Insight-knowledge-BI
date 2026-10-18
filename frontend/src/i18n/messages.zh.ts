/**
 * Chinese message pack.
 *
 * Key convention: `<domain>.<section>.<key>`. Placeholders use `{name}` form so
 * the runtime interpolator can stay dependency-free.
 *
 * Entries live in `packs/zh.core.ts` (shared shell) and `packs/zh.domain.ts`
 * (product surface) so no single module grows past the file-size budget.
 */
import { zhCore } from './packs/zh.core';
import { zhDomain } from './packs/zh.domain';
import { zhAi } from './packs/zh.ai';

export const zh = { ...zhCore, ...zhDomain, ...zhAi } as const;

/** Source of truth for the key space; `messages.en.ts` must mirror it. */
export type TranslationKey = keyof typeof zh;
