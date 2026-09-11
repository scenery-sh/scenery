"""Recompute only from archived spans; never starts builds, tests or runtimes."""
import json,datetime,re,statistics,csv
from pathlib import Path
root=Path(__file__).parent
raw=json.loads((root/'timelines.json').read_text())
def ns(text):
 m=re.fullmatch(r'(.*T\d\d:\d\d:\d\d)(?:\.(\d+))?([+-]\d\d:\d\d|Z)',text)
 base=datetime.datetime.fromisoformat(m[1]+m[3].replace('Z','+00:00'))
 return int(base.timestamp())*10**9+int((m[2] or '').ljust(9,'0'))
def interval(s):return ns(s['StartedAt']),ns(s['StartedAt'])+s['Duration']
rows=[]
for section in ['samples','startup']:
 counts={'baseline':0,'candidate':0}
 for item in raw[section]:
  mode=item['mode'];counts[mode]+=1;r=item['record'];sp=r['Steps'];by={s['Name']:s for s in sp if s['Name'].startswith('audit.')}
  row={'section':section,'pair':counts[mode],'mode':mode,'total_ms':item['elapsed_ms']}
  cs,ce=interval(by['audit.compile_admission'])
  builds=[s for s in sp if s['Name']=='go.command' and s['Reason']=='build']
  checks=[s for s in sp if s['Name']=='implementation.check']
  if section=='samples':
   assert len(builds)==len(checks)==1
   bundle=next(s for s in sp if s['Name']=='runtime.bundle')
   js=min(interval(bundle)[0],interval(checks[0])[0]);je=max(interval(builds[0])[1],interval(checks[0])[1]);assert cs<=js<=je<=ce
   row.update(pre_join_ms=(js-cs)/1e6,join_ms=(je-js)/1e6,post_join_ms=(ce-je)/1e6,build_only_ms=builds[0]['Duration']/1e6,verifier_only_ms=checks[0]['Duration']/1e6,verifier_finish_before_build_ms=(interval(builds[0])[1]-interval(checks[0])[1])/1e6)
  else:
   assert not builds and not checks
   row['cache_compile_admission_ms']=(ce-cs)/1e6
  for name in ['retain','preflight','handoff','release_preparation']:
   row[name+'_ms']=by['audit.'+name]['Duration']/1e6
  categories=['pre_join_ms','join_ms','post_join_ms'] if section=='samples' else ['cache_compile_admission_ms']
  categories+=['retain_ms','preflight_ms','handoff_ms','release_preparation_ms']
  row['other_ms']=row['total_ms']-sum(row[c] for c in categories)
  assert row['other_ms']>=0
  # Top-level phase intervals must not overlap, allowing wall/monotonic rounding below 50 us.
  top=sorted(interval(s) for s in by.values());assert all(a[1]<=b[0]+50000 for a,b in zip(top,top[1:]))
  rows.append(row)
paired=[];summary={}
for section in ['samples','startup']:
 subset=[r for r in rows if r['section']==section];keys=[k for k in subset[0] if k.endswith('_ms')]
 for i in range(1,7):
  a=next(r for r in subset if r['pair']==i and r['mode']=='baseline');b=next(r for r in subset if r['pair']==i and r['mode']=='candidate');paired.append({'section':section,'pair':i,**{k:b[k]-a[k] for k in keys}})
 summary[section]={k:{'ordinary_median':statistics.median(r[k] for r in subset if r['mode']=='baseline'),'direct_median':statistics.median(r[k] for r in subset if r['mode']=='candidate'),'paired_delta_median':statistics.median(r[k] for r in paired if r['section']==section),'paired_delta_mean':statistics.mean(r[k] for r in paired if r['section']==section)} for k in keys}
(root/'analysis.json').write_text(json.dumps({'sign':'direct minus ordinary; positive is slower','method':'non-overlapping parent decomposition; join envelope includes bundle + build branch and concurrent verifier once; uninstrumented post-join work stays unresolved','rows':rows,'paired':paired,'summary':summary},indent=2)+'\n')
for section in ['samples','startup']:
 values=[r for r in paired if r['section']==section]
 with (root/(section+'-paired.csv')).open('w') as f:
  w=csv.DictWriter(f,fieldnames=list(values[0]));w.writeheader();w.writerows(values)
 print(section,json.dumps(summary[section],indent=1))
