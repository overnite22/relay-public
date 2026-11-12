import {useEffect,useState,type FormEvent} from "react";
import {Users,KeyRound,Plus} from "lucide-react";
import {localAuthApi,type LocalUser} from "../api";
import {Modal} from "./Modal";

export function UsersDialog({current,onClose}:{current:string;onClose:()=>void}){
 const [users,setUsers]=useState<LocalUser[]>([]);const [selected,setSelected]=useState<LocalUser|null>(null);
 const [email,setEmail]=useState("");const [password,setPassword]=useState("");const [admin,setAdmin]=useState(false);const [disabled,setDisabled]=useState(false);
 const [error,setError]=useState("");const [busy,setBusy]=useState(false);
 const load=()=>localAuthApi.users().then(setUsers).catch(e=>setError(e.message));useEffect(()=>{void load()},[]);
 const choose=(u:LocalUser|null)=>{setSelected(u);setEmail(u?.email??"");setPassword("");setAdmin(u?.admin??false);setDisabled(u?.disabled??false);setError("")};
 const save=async(e:FormEvent)=>{e.preventDefault();setError("");setBusy(true);try{
  if(selected)await localAuthApi.update(selected.email,{admin,disabled,...(password?{password}:{})});else await localAuthApi.create(email,password,admin);
  setPassword("");if(selected?.email===current){location.reload();return}choose(null);await load();
 }catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 return <Modal title="Comptes locaux" icon={<Users size={17}/>} onClose={onClose} width={760}>
  <div className="users-layout"><div className="users-list"><button className="btn" onClick={()=>choose(null)}><Plus size={15}/> Nouveau compte</button>
  {users.map(u=><button className={`user-row ${selected?.email===u.email?"selected":""}`} key={u.email} onClick={()=>choose(u)}><strong>{u.email}</strong><span>{u.disabled?"Désactivé":u.admin?"Administrateur":"Utilisateur"}</span></button>)}</div>
  <form onSubmit={save} className="users-editor"><h3>{selected?"Propriétés du compte":"Nouveau compte"}</h3>
   <label>Adresse e-mail<input type="email" value={email} onChange={e=>setEmail(e.target.value)} disabled={!!selected} required maxLength={254}/></label>
   <label>{selected?"Nouveau mot de passe (facultatif)":"Mot de passe"}<input type="password" autoComplete="new-password" value={password} onChange={e=>setPassword(e.target.value)} required={!selected} minLength={12} maxLength={1024}/></label>
   <label className="check"><input type="checkbox" checked={admin} onChange={e=>setAdmin(e.target.checked)}/> Administrateur de l’instance</label>
   {selected&&<label className="check"><input type="checkbox" checked={disabled} onChange={e=>setDisabled(e.target.checked)}/> Compte désactivé</label>}
   <p className="muted">Les utilisateurs partagent le coffre et les connexions. Les administrateurs gèrent aussi les comptes et les sauvegardes.</p>
   <p className="muted">Désactiver un compte ou changer son mot de passe révoque ses sessions et ses appareils.</p>
   {error&&<p className="error" role="alert">{error}</p>}<button className="btn primary" disabled={busy}>{selected?"Enregistrer":"Créer le compte"}</button>
  </form></div>
 </Modal>
}
export function PasswordDialog({onClose}:{onClose:()=>void}){
 const [current,setCurrent]=useState("");const [password,setPassword]=useState("");const [repeat,setRepeat]=useState("");const [error,setError]=useState("");const [busy,setBusy]=useState(false);
 const save=async(e:FormEvent)=>{e.preventDefault();setError("");if(password!==repeat){setError("Les mots de passe ne correspondent pas.");return}setBusy(true);try{await localAuthApi.password(current,password);location.reload()}catch(e){setError((e as Error).message)}finally{setBusy(false)}};
 return <Modal title="Changer mon mot de passe" icon={<KeyRound size={17}/>} onClose={onClose} width={440}><form onSubmit={save} className="users-editor">
  <label>Mot de passe actuel<input type="password" autoComplete="current-password" value={current} onChange={e=>setCurrent(e.target.value)} required maxLength={1024}/></label>
  <label>Nouveau mot de passe<input type="password" autoComplete="new-password" value={password} onChange={e=>setPassword(e.target.value)} required minLength={12} maxLength={1024}/></label>
  <label>Confirmer le mot de passe<input type="password" autoComplete="new-password" value={repeat} onChange={e=>setRepeat(e.target.value)} required maxLength={1024}/></label>
  <p className="muted">Tu devras te reconnecter sur tes appareils.</p>{error&&<p className="error" role="alert">{error}</p>}<button className="btn primary" disabled={busy}>Enregistrer</button>
 </form></Modal>
}
