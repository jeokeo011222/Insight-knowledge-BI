import { createAsyncThunk, createSlice } from '@reduxjs/toolkit';
import { client, endpoints, errorMessage, tokenStore } from '../api/client';
import { tGlobal } from '../i18n';
import type { MeUser, ResultEnvelope, TokenPair } from '../api/types';

export type UserStatus = 'idle' | 'loading' | 'authenticated' | 'anonymous';

export interface UserState {
  me: MeUser | null;
  status: UserStatus;
  error: string | null;
}

const initialState: UserState = {
  me: null,
  status: 'idle',
  error: null,
};

export interface LoginArgs {
  username: string;
  password: string;
}

/** Authenticates, stores the token pair and loads the current user. */
export const login = createAsyncThunk<MeUser, LoginArgs, { rejectValue: string }>(
  'user/login',
  async ({ username, password }, { rejectWithValue }) => {
    try {
      const pair = await client.post<TokenPair>(
        endpoints.login,
        { username, password, provider: 'db' },
        { anonymous: true },
      );
      if (!pair.access_token) {
        return rejectWithValue(tGlobal('error.auth.missing_token'));
      }
      tokenStore.set(pair);
      const me = await client.get<ResultEnvelope<MeUser>>(endpoints.me, undefined, {
        retried: true,
      });
      if (!me.result || me.result.is_anonymous) {
        tokenStore.clear();
        return rejectWithValue(tGlobal('error.auth.me_failed'));
      }
      return me.result;
    } catch (error) {
      tokenStore.clear();
      return rejectWithValue(errorMessage(error));
    }
  },
);

/** Clears the server cookie and local tokens. */
export const logout = createAsyncThunk<void>('user/logout', async () => {
  try {
    await client.post(endpoints.logout, undefined, { anonymous: true });
  } catch {
    // Logout must succeed locally even when the server call fails.
  } finally {
    tokenStore.clear();
  }
});

/** Loads /me/ for an existing access token. */
export const fetchMe = createAsyncThunk<MeUser | null, void, { rejectValue: string }>(
  'user/fetchMe',
  async (_, { rejectWithValue }) => {
    if (!tokenStore.access) return null;
    try {
      const response = await client.get<ResultEnvelope<MeUser>>(endpoints.me);
      if (!response.result || response.result.is_anonymous) {
        tokenStore.clear();
        return null;
      }
      return response.result;
    } catch (error) {
      return rejectWithValue(errorMessage(error));
    }
  },
);

const userSlice = createSlice({
  name: 'user',
  initialState,
  reducers: {
    clearError(state) {
      state.error = null;
    },
  },
  extraReducers: builder => {
    builder
      .addCase(login.pending, state => {
        state.status = 'loading';
        state.error = null;
      })
      .addCase(login.fulfilled, (state, action) => {
        state.me = action.payload;
        state.status = 'authenticated';
        state.error = null;
      })
      .addCase(login.rejected, (state, action) => {
        state.me = null;
        state.status = 'anonymous';
        state.error = action.payload ?? tGlobal('error.auth.login_failed');
      })
      .addCase(fetchMe.pending, state => {
        state.status = 'loading';
      })
      .addCase(fetchMe.fulfilled, (state, action) => {
        state.me = action.payload;
        state.status = action.payload ? 'authenticated' : 'anonymous';
      })
      .addCase(fetchMe.rejected, (state, action) => {
        state.me = null;
        state.status = 'anonymous';
        state.error = action.payload ?? null;
      })
      .addCase(logout.fulfilled, state => {
        state.me = null;
        state.status = 'anonymous';
        state.error = null;
      })
      .addCase(logout.rejected, state => {
        state.me = null;
        state.status = 'anonymous';
        state.error = null;
      });
  },
});

export const { clearError } = userSlice.actions;
export default userSlice.reducer;
