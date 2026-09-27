// Session-scoped only: nothing sensitive is persisted in browser storage.
// A new login user object cannot reuse another session's cached statistics.
const sessions = new WeakMap<object, Map<string, {data:unknown;time:number}>>()
export function overviewCache<T>(user:object|null|undefined):Map<string,{data:T;time:number}> {
 if(!user)return new Map()
 let cache=sessions.get(user)
 if(!cache){cache=new Map();sessions.set(user,cache)}
 return cache as Map<string,{data:T;time:number}>
}
