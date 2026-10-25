"""Reward goals derived exclusively from account-scoped Twitch snapshots."""
import datetime as dt
import time


def timestamp(value):
    try:
        return dt.datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp()
    except (ValueError, TypeError, AttributeError):
        return None


def default_goal():
    return {'mode': 'all', 'rewards': [], 'lockedAt': None}


def achieved(drop):
    minutes, required = drop.get('minutes'), drop.get('requiredMinutes')
    return drop.get('claimed') is True or drop.get('claimable') is True or (
        isinstance(minutes, (int, float)) and isinstance(required, (int, float))
        and required > 0 and minutes >= required)


def capture(task, campaigns, sampled_at):
    goal = task.get('goal') or default_goal()
    if goal['mode'] != 'all' or goal['rewards']:
        return goal
    active = [c for c in campaigns if c['gameId'] == str(task['game'].get('id'))
              and (timestamp(c.get('startsAt')) or 0) <= time.time()
              < (timestamp(c.get('endsAt')) or 0) and c.get('drops')]
    # Freeze one round only after actual reward metadata has arrived.
    rewards = [{'campaignId': c['id'], 'dropId': d['id']} for c in active for d in c['drops'] if d['id']]
    return {'mode': 'all', 'rewards': rewards, 'lockedAt': sampled_at if rewards else None}


def evaluate(task, campaigns, readable=True):
    goal = task.get('goal') or default_goal()
    refs = goal['rewards']
    catalogue = {c['id']: c for c in campaigns if c['gameId'] == str(task['game'].get('id'))}
    rows, runnable = [], set()
    blocked_count = 0
    for ref in refs:
        campaign = catalogue.get(ref['campaignId'])
        drops = {d['id']: d for d in campaign['drops']} if campaign else {}
        drop = drops.get(ref['dropId'])
        row = {**ref, 'name': (drop or {}).get('name') or '等待奖励资料',
               'campaignName': (campaign or {}).get('name') or '等待活动资料',
               'completed': bool(drop and achieved(drop)), 'claimPending': bool(
                   drop and achieved(drop) and drop.get('claimed') is not True)}
        if row['completed']:
            row['status'] = 'completed'
        elif not drop:
            row['status'] = 'unknown'
        elif (timestamp(campaign.get('endsAt')) or float('inf')) <= time.time():
            row['status'] = 'expired'
        elif (timestamp(campaign.get('startsAt')) or 0) > time.time():
            row['status'] = 'upcoming'
        else:
            # Follow only this goal's dependency chain. An unrelated unclaimed
            # reward must not block another independent, runnable reward.
            def dependency_state(node, path):
                if node['id'] in path:
                    return 'unknown'
                states = []
                for prerequisite in node.get('prerequisites', []):
                    parent = drops.get(prerequisite)
                    if not parent:
                        states.append('unknown')
                    elif parent.get('claimed') is True:
                        continue
                    elif achieved(parent):
                        states.append('waiting_claim')
                    else:
                        states.append(dependency_state(parent, path | {node['id']}))
                if not states or 'ready' in states:
                    return 'ready'
                return 'unknown' if 'unknown' in states else 'waiting_claim'
            row['status'] = dependency_state(drop, set())
            if row['status'] == 'ready':
                runnable.add(campaign['id'])
            elif row['status'] == 'waiting_claim':
                blocked_count += 1
        rows.append(row)
    completed = sum(r['completed'] for r in rows)
    if not refs:
        status = 'waiting_targets'
    elif completed == len(refs):
        status = 'completed'
    elif runnable:
        status = 'ready' if readable else 'unknown'
    elif blocked_count:
        status = 'waiting_claim'
    elif all(r['completed'] or r['status'] == 'expired' for r in rows):
        status = 'expired'
    else:
        status = 'unknown'
    return {'taskId': task['id'], 'status': status, 'total': len(refs), 'completed': completed,
            'claimPending': sum(r['claimPending'] for r in rows), 'blocked': blocked_count,
            'runnableCampaignIds': sorted(runnable), 'rewards': rows}


def merge_progress(fresh, previous):
    """Sparse metadata must not erase previously confirmed account progress."""
    old_drops = {d['id']: d for d in previous.get('drops', [])}
    for drop in fresh['drops']:
        old = old_drops.get(drop['id'], {})
        for key in ['minutes', 'requiredMinutes', 'claimed']:
            if drop.get(key) is None and old.get(key) is not None:
                drop[key] = old[key]
        if old.get('minutes') is not None and drop.get('minutes') is not None:
            drop['minutes'] = max(old['minutes'], drop['minutes'])
        if old.get('claimed') is True:
            drop['claimed'] = True
        drop['claimable'] = drop['claimed'] is not True and (drop['claimable'] or old.get('claimable', False))
        if old.get('sampledAt') and drop.get('minutes') == old.get('minutes') and drop.get('claimed') == old.get('claimed'):
            drop['sampledAt'] = old['sampledAt']
    return fresh
