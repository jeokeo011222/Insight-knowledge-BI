import { configureStore } from '@reduxjs/toolkit';
import { useDispatch, useSelector } from 'react-redux';
import { useMemo } from 'react';
import userReducer from './userSlice';
import aiReducer from './aiSlice';

export const store = configureStore({
  reducer: {
    user: userReducer,
    ai: aiReducer,
  },
  middleware: getDefaultMiddleware =>
    getDefaultMiddleware({ serializableCheck: { ignoredActionPaths: ['meta.arg'] } }),
});

export type RootState = ReturnType<typeof store.getState>;
export type AppDispatch = typeof store.dispatch;

export const useAppDispatch = useDispatch.withTypes<AppDispatch>();
export const useAppSelector = useSelector.withTypes<RootState>();

export interface PermissionChecker {
  isAdmin: boolean;
  can: (action: string, resource?: string) => boolean;
  permissions: string[];
}

/**
 * RBAC helper mirroring Superset's `can(action, resource)`.
 * The Admin role carries the wildcard permission `* on *`.
 */
export function usePermission(): PermissionChecker {
  const me = useAppSelector(state => state.user.me);

  return useMemo<PermissionChecker>(() => {
    const permissions = me?.permissions ?? [];
    const isAdmin = permissions.includes('* on *');
    const can = (action: string, resource?: string): boolean => {
      if (!me || me.is_anonymous) return false;
      if (isAdmin) return true;
      if (!resource) {
        return permissions.some(permission => permission.startsWith(`${action} on `));
      }
      return permissions.includes(`${action} on ${resource}`);
    };
    return { isAdmin, can, permissions };
  }, [me]);
}
