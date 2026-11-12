import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { KeyRound, Loader2, ShieldCheck } from "lucide-react";
import { api, ApiError, localAuthApi } from "../api";

export function AuthGate({children}:{children:ReactNode}) {
 const [state,setState]=useState<"loading"|"ready"|"login"|"setup"|"error">("loading");
 const [email,setEmail]=useState("");const [password,setPassword]=useState("");const [token,setToken]=useState("");
 const [error,setError]=useState("");const [busy,setBusy]=useState(false);
 useEffect(()=>{let disposed=false;(async()=>{
  const status=await localAuthApi.status();if(disposed)return;
  if(status.mode!=="local"){setState("ready");return}
  if(!status.initialized){setState("setup");return}
  try{await api.me();if(!disposed)setState("ready")}
  catch(e){if(disposed)return;if(e instanceof ApiError&&e.status===401)setState("login");else throw e}
 })().catch((e:Error)=>{if(!disposed){setError(e.message);setState("error")}});return()=>{disposed=true}},[]);
 const submit=async(e:FormEvent)=>{e.preventDefault();setBusy(true);setError("");try{
  if(state==="setup")await localAuthApi.setup(email,password,token.trim());else await localAuthApi.login(email,password);
  setPassword("");setToken("");location.reload();
 }catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 if(state==="ready")return <>{children}</>;
 return <div className="auth-page">
  <header className="titlebar"><img src="/favicon.svg" alt="" className="logo"/><span className="app-name">Relay</span><span className="title-sep">—</span><span>Gestionnaire de connexions</span></header>
  <main className="auth-center"><section className="auth-card">
   <div className="auth-card-heading">{state==="setup"?<ShieldCheck size={22}/>:<KeyRound size={22}/>}<div><h1>{state==="setup"?"Créer le premier administrateur":"Connexion à Relay"}</h1><p>Comptes locaux</p></div></div>
   {state==="loading"?<p className="muted"><Loader2 className="spin" size={16}/> Chargement…</p>:state==="error"?<><p className="error">{error}</p><button className="btn" onClick={()=>location.reload()}>Réessayer</button></>:<form onSubmit={submit}>
    {state==="setup"&&<div className="auth-setup"><p>Récupère le code sur ton serveur :</p><code>docker compose exec relay /relay setup-token</code><label>Code d’initialisation<input type="password" autoComplete="off" value={token} onChange={e=>setToken(e.target.value)} required maxLength={100}/></label></div>}
    <label>Adresse e-mail<input type="email" name="email" autoComplete="username" value={email} onChange={e=>setEmail(e.target.value)} required maxLength={254} autoFocus/></label>
    <label>Mot de passe<input type="password" name="password" autoComplete={state==="setup"?"new-password":"current-password"} value={password} onChange={e=>setPassword(e.target.value)} required minLength={state==="setup"?12:undefined} maxLength={1024}/></label>
    {state==="setup"&&<p className="muted">Utilise au moins 12 caractères.</p>}
    {error&&<p className="error" role="alert">{error}</p>}
    <button className="btn primary" type="submit" disabled={busy}>{busy&&<Loader2 size={15} className="spin"/>}{state==="setup"?"Initialiser Relay":"Se connecter"}</button>
   </form>}
  </section></main>
 </div>;
}
