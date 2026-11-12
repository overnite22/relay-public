import fs from 'node:fs/promises';
import path from 'node:path';
const output=[];
async function collect(directory) {
 for(const entry of await fs.readdir(directory,{withFileTypes:true})) {
  if(!entry.isDirectory()||entry.name.startsWith('.')) continue;
  const folder=path.join(directory,entry.name);
  if(entry.name.startsWith('@')){await collect(folder);continue;}
  try {
   const pkg=JSON.parse(await fs.readFile(path.join(folder,'package.json'),'utf8'));
   output.push('\n=== '+pkg.name+'@'+pkg.version+' — '+(pkg.license??'see package license')+' ===\n');
   for(const name of (await fs.readdir(folder)).filter(n=>/^(licen[sc]e|copying|notice)([.-]|$)/i.test(n))) {
    if((await fs.stat(path.join(folder,name))).isFile()) output.push(await fs.readFile(path.join(folder,name),'utf8'));
   }
  } catch(error){if(error.code!=='ENOENT')throw error;}
 }
}
await collect(path.resolve(process.argv[2]));
await fs.writeFile(process.argv[3],output.join('\n'));
