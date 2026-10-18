/**
 * English message pack. Typed as `Record<TranslationKey, string>` so a key that
 * exists in the Chinese pack but is missing here fails the type check.
 *
 * Entries live in `packs/en.core.ts` (shared shell) and `packs/en.domain.ts`
 * (product surface) so no single module grows past the file-size budget.
 */
import type { TranslationKey } from './messages.zh';
import { enCore } from './packs/en.core';
import { enDomain } from './packs/en.domain';
import { enAi } from './packs/en.ai';

export const en: Record<TranslationKey, string> = { ...enCore, ...enDomain, ...enAi };
