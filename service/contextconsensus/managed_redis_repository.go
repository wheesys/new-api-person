package contextconsensus

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
)

const (
	managedRedisRevisionField      = "revision"
	managedRedisFencingField       = "fencing_token"
	managedRedisPayloadField       = "encrypted_payload"
	managedRedisBindingDigestField = "binding_digest"
)

var (
	managedAcquireLeaseScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then
  return -1
end
local fencing_token = redis.call('INCR', KEYS[2])
redis.call('PSETEX', KEYS[1], ARGV[2], ARGV[1] .. ':' .. fencing_token)
redis.call('PEXPIRE', KEYS[2], ARGV[3])
return fencing_token
`)
	managedRenewLeaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return 0
end
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return 1
`)
	managedReleaseLeaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return 0
end
redis.call('DEL', KEYS[1])
return 1
`)
	managedCompareAndSwapScript = redis.NewScript(`
if redis.call('GET', KEYS[2]) ~= ARGV[1] then
  return -1
end
local current_revision = redis.call('HGET', KEYS[1], 'revision')
if not current_revision then
  current_revision = '0'
end
if current_revision ~= ARGV[2] then
  return -2
end
redis.call('HSET', KEYS[1],
  'revision', ARGV[3],
  'fencing_token', ARGV[4],
  'encrypted_payload', ARGV[5])
redis.call('PEXPIRE', KEYS[1], ARGV[6])
redis.call('PEXPIRE', KEYS[3], ARGV[7])
return tonumber(ARGV[3])
`)
	managedCompareAndSwapMigrateScript = redis.NewScript(`
if redis.call('GET', KEYS[2]) ~= ARGV[1] then
  return -1
end
local previous_revision = redis.call('HGET', KEYS[1], 'revision')
if not previous_revision or previous_revision ~= ARGV[2] then
  return -2
end
if redis.call('EXISTS', KEYS[4]) == 1 or redis.call('EXISTS', KEYS[6]) == 1 then
  return -3
end
redis.call('HSET', KEYS[4],
  'revision', ARGV[3],
  'fencing_token', ARGV[4],
  'encrypted_payload', ARGV[5])
redis.call('PEXPIRE', KEYS[4], ARGV[6])
local active_fence = redis.call('GET', KEYS[5])
if not active_fence or tonumber(active_fence) < tonumber(ARGV[4]) then
  redis.call('SET', KEYS[5], ARGV[4])
end
redis.call('PEXPIRE', KEYS[5], ARGV[7])
redis.call('PEXPIRE', KEYS[3], ARGV[7])
redis.call('DEL', KEYS[1])
return tonumber(ARGV[3])
`)
	managedCompareAndSwapWithProviderStateScript = redis.NewScript(`
if redis.call('GET', KEYS[2]) ~= ARGV[1] then
  return -1
end
local current_revision = redis.call('HGET', KEYS[1], 'revision')
if not current_revision then current_revision = '0' end
if current_revision ~= ARGV[2] then return -2 end
for index = 5, #KEYS do
  if redis.call('EXISTS', KEYS[index]) == 1 then return -4 end
end
local current_digest = redis.call('HGET', KEYS[4], 'binding_digest')
if current_digest and current_digest ~= ARGV[8] then return -4 end
redis.call('HSET', KEYS[1], 'revision', ARGV[3], 'fencing_token', ARGV[4], 'encrypted_payload', ARGV[5])
redis.call('PEXPIRE', KEYS[1], ARGV[6])
redis.call('PEXPIRE', KEYS[3], ARGV[7])
redis.call('HSET', KEYS[4], 'binding_digest', ARGV[8], 'encrypted_payload', ARGV[9])
redis.call('PEXPIRE', KEYS[4], ARGV[10])
return tonumber(ARGV[3])
`)
	managedCompareAndSwapMigrateWithProviderStateScript = redis.NewScript(`
if redis.call('GET', KEYS[2]) ~= ARGV[1] then return -1 end
local previous_revision = redis.call('HGET', KEYS[1], 'revision')
if not previous_revision or previous_revision ~= ARGV[2] then return -2 end
if redis.call('EXISTS', KEYS[4]) == 1 or redis.call('EXISTS', KEYS[6]) == 1 then return -3 end
for index = 8, #KEYS do
  if redis.call('EXISTS', KEYS[index]) == 1 then return -4 end
end
local current_digest = redis.call('HGET', KEYS[7], 'binding_digest')
if current_digest and current_digest ~= ARGV[8] then return -4 end
redis.call('HSET', KEYS[4], 'revision', ARGV[3], 'fencing_token', ARGV[4], 'encrypted_payload', ARGV[5])
redis.call('PEXPIRE', KEYS[4], ARGV[6])
local active_fence = redis.call('GET', KEYS[5])
if not active_fence or tonumber(active_fence) < tonumber(ARGV[4]) then redis.call('SET', KEYS[5], ARGV[4]) end
redis.call('PEXPIRE', KEYS[5], ARGV[7])
redis.call('PEXPIRE', KEYS[3], ARGV[7])
redis.call('HSET', KEYS[7], 'binding_digest', ARGV[8], 'encrypted_payload', ARGV[9])
redis.call('PEXPIRE', KEYS[7], ARGV[10])
redis.call('DEL', KEYS[1])
return tonumber(ARGV[3])
`)
	managedDeleteConsensusScript = redis.NewScript(`
if redis.call('GET', KEYS[2]) ~= ARGV[1] then
  return -1
end
local current_revision = redis.call('HGET', KEYS[1], 'revision')
if not current_revision then
  return -3
end
if current_revision ~= ARGV[2] then
  return -2
end
redis.call('DEL', KEYS[1])
return 1
`)
	managedRegisterProviderBindingScript = redis.NewScript(`
local current_digest = redis.call('HGET', KEYS[1], 'binding_digest')
if current_digest and current_digest ~= ARGV[1] then
  return -1
end
redis.call('HSET', KEYS[1],
  'binding_digest', ARGV[1],
  'encrypted_payload', ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return 1
`)
	managedDeleteProviderBindingScript = redis.NewScript(`
local current_digest = redis.call('HGET', KEYS[1], 'binding_digest')
if not current_digest then
  return -2
end
if current_digest ~= ARGV[1] then
  return -1
end
redis.call('DEL', KEYS[1])
return 1
`)
)

// RedisManagedConsensusRepository is a dedicated encrypted-state repository.
// It intentionally bypasses common.RedisSet so ciphertext is never printed in
// debug logs, and all mutating concurrency checks execute in Redis Lua scripts.
type RedisManagedConsensusRepository struct {
	client                  *redis.Client
	maximumRetention        time.Duration
	fencingCounterRetention time.Duration
	now                     func() time.Time
}

func NewRedisManagedConsensusRepository(client *redis.Client, maximumRetention time.Duration) (*RedisManagedConsensusRepository, error) {
	if client == nil {
		return nil, fmt.Errorf("managed consensus Redis client is required")
	}
	if maximumRetention <= 0 || maximumRetention > time.Duration(math.MaxInt64/2) {
		return nil, fmt.Errorf("managed consensus maximum retention must be positive and bounded")
	}
	return &RedisManagedConsensusRepository{
		client:                  client,
		maximumRetention:        maximumRetention,
		fencingCounterRetention: maximumRetention * 2,
		now:                     time.Now,
	}, nil
}

func (repository *RedisManagedConsensusRepository) LoadConsensus(ctx context.Context, key ManagedConversationStorageKey) (ManagedConsensusRecord, error) {
	if err := repository.validateConversationKey(key); err != nil {
		return ManagedConsensusRecord{}, err
	}
	fields, err := repository.client.HGetAll(ctx, key.RepositoryKey).Result()
	if err != nil {
		return ManagedConsensusRecord{}, fmt.Errorf("load managed consensus metadata: %w", err)
	}
	if len(fields) == 0 {
		return ManagedConsensusRecord{}, ErrManagedConsensusNotFound
	}
	ttl, err := repository.client.PTTL(ctx, key.RepositoryKey).Result()
	if err != nil {
		return ManagedConsensusRecord{}, fmt.Errorf("load managed consensus TTL: %w", err)
	}
	if ttl <= 0 {
		return ManagedConsensusRecord{}, fmt.Errorf("managed consensus state has no valid TTL")
	}
	revision, err := strconv.ParseUint(fields[managedRedisRevisionField], 10, 64)
	if err != nil || revision == 0 {
		return ManagedConsensusRecord{}, fmt.Errorf("managed consensus revision is invalid")
	}
	fencingToken, err := strconv.ParseUint(fields[managedRedisFencingField], 10, 64)
	if err != nil || fencingToken == 0 {
		return ManagedConsensusRecord{}, fmt.Errorf("managed consensus fencing token is invalid")
	}
	var payload ManagedEncryptedEnvelope
	if err := common.UnmarshalJsonStr(fields[managedRedisPayloadField], &payload); err != nil {
		return ManagedConsensusRecord{}, fmt.Errorf("decode encrypted managed consensus envelope: %w", err)
	}
	return ManagedConsensusRecord{
		Revision:     revision,
		FencingToken: fencingToken,
		Payload:      payload,
		ExpiresAt:    repository.now().Add(ttl),
	}, nil
}

func (repository *RedisManagedConsensusRepository) AcquireConsensusLease(ctx context.Context, key ManagedConversationStorageKey, holderID string, ttl time.Duration) (ManagedConsensusLease, error) {
	if err := repository.validateConversationKey(key); err != nil {
		return ManagedConsensusLease{}, err
	}
	if strings.TrimSpace(holderID) == "" {
		return ManagedConsensusLease{}, fmt.Errorf("managed consensus lease holder is required")
	}
	if err := repository.validateTTL(ttl); err != nil {
		return ManagedConsensusLease{}, err
	}
	leaseKey := key.RepositoryKey + ":lease"
	fencingKey := key.RepositoryKey + ":fence"
	result, err := managedAcquireLeaseScript.Run(ctx, repository.client, []string{leaseKey, fencingKey}, holderID, ttl.Milliseconds(), repository.fencingCounterRetention.Milliseconds()).Int64()
	if err != nil {
		return ManagedConsensusLease{}, fmt.Errorf("acquire managed consensus lease: %w", err)
	}
	if result == -1 {
		return ManagedConsensusLease{}, ErrManagedConsensusLeaseHeld
	}
	if result <= 0 {
		return ManagedConsensusLease{}, fmt.Errorf("managed consensus fencing token is invalid")
	}
	return ManagedConsensusLease{
		RepositoryKey: key.RepositoryKey,
		HolderID:      holderID,
		FencingToken:  uint64(result),
		ExpiresAt:     repository.now().Add(ttl),
	}, nil
}

func (repository *RedisManagedConsensusRepository) RenewConsensusLease(ctx context.Context, lease ManagedConsensusLease, ttl time.Duration) (ManagedConsensusLease, error) {
	if err := repository.validateLease(lease); err != nil {
		return ManagedConsensusLease{}, err
	}
	if err := repository.validateTTL(ttl); err != nil {
		return ManagedConsensusLease{}, err
	}
	result, err := managedRenewLeaseScript.Run(ctx, repository.client, []string{lease.RepositoryKey + ":lease"}, managedLeaseValue(lease), ttl.Milliseconds()).Int64()
	if err != nil {
		return ManagedConsensusLease{}, fmt.Errorf("renew managed consensus lease: %w", err)
	}
	if result != 1 {
		return ManagedConsensusLease{}, ErrManagedConsensusLeaseInvalid
	}
	lease.ExpiresAt = repository.now().Add(ttl)
	return lease, nil
}

func (repository *RedisManagedConsensusRepository) ReleaseConsensusLease(ctx context.Context, lease ManagedConsensusLease) error {
	if err := repository.validateLease(lease); err != nil {
		return err
	}
	result, err := managedReleaseLeaseScript.Run(ctx, repository.client, []string{lease.RepositoryKey + ":lease"}, managedLeaseValue(lease)).Int64()
	if err != nil {
		return fmt.Errorf("release managed consensus lease: %w", err)
	}
	if result != 1 {
		return ErrManagedConsensusLeaseInvalid
	}
	return nil
}

func (repository *RedisManagedConsensusRepository) CompareAndSwapConsensus(ctx context.Context, key ManagedConversationStorageKey, expectedRevision uint64, lease ManagedConsensusLease, payload ManagedEncryptedEnvelope, ttl time.Duration) (ManagedConsensusRecord, error) {
	if err := repository.validateConversationKey(key); err != nil {
		return ManagedConsensusRecord{}, err
	}
	if err := repository.validateLeaseForKey(key, lease); err != nil {
		return ManagedConsensusRecord{}, err
	}
	if expectedRevision == math.MaxUint64 {
		return ManagedConsensusRecord{}, fmt.Errorf("managed consensus revision overflow")
	}
	if payload.Purpose != ManagedEncryptionPurposeConsensusState || payload.Revision != expectedRevision+1 {
		return ManagedConsensusRecord{}, fmt.Errorf("encrypted consensus payload revision or purpose does not match CAS")
	}
	if err := repository.validateTTL(ttl); err != nil {
		return ManagedConsensusRecord{}, err
	}
	encodedPayload, err := common.Marshal(payload)
	if err != nil {
		return ManagedConsensusRecord{}, fmt.Errorf("encode encrypted managed consensus envelope: %w", err)
	}
	result, err := managedCompareAndSwapScript.Run(
		ctx,
		repository.client,
		[]string{key.RepositoryKey, key.RepositoryKey + ":lease", key.RepositoryKey + ":fence"},
		managedLeaseValue(lease),
		strconv.FormatUint(expectedRevision, 10),
		strconv.FormatUint(expectedRevision+1, 10),
		strconv.FormatUint(lease.FencingToken, 10),
		string(encodedPayload),
		ttl.Milliseconds(),
		repository.fencingCounterRetention.Milliseconds(),
	).Int64()
	if err != nil {
		return ManagedConsensusRecord{}, fmt.Errorf("compare and swap managed consensus: %w", err)
	}
	switch result {
	case -1:
		return ManagedConsensusRecord{}, ErrManagedConsensusLeaseInvalid
	case -2:
		return ManagedConsensusRecord{}, ErrManagedConsensusRevisionConflict
	}
	if result <= 0 || uint64(result) != expectedRevision+1 {
		return ManagedConsensusRecord{}, fmt.Errorf("managed consensus CAS returned an invalid revision")
	}
	return ManagedConsensusRecord{
		Revision:     uint64(result),
		FencingToken: lease.FencingToken,
		Payload:      payload,
		ExpiresAt:    repository.now().Add(ttl),
	}, nil
}

func (repository *RedisManagedConsensusRepository) CompareAndSwapConsensusWithProviderState(
	ctx context.Context,
	key ManagedConversationStorageKey,
	expectedRevision uint64,
	lease ManagedConsensusLease,
	consensusPayload ManagedEncryptedEnvelope,
	providerKey ManagedProviderStateStorageKey,
	providerConflictKeys []ManagedProviderStateStorageKey,
	bindingDigest string,
	providerPayload ManagedEncryptedEnvelope,
	consensusTTL time.Duration,
	providerTTL time.Duration,
) (ManagedConsensusRecord, ManagedProviderStateRecord, error) {
	if err := repository.validateConversationKey(key); err != nil {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, err
	}
	if err := repository.validateLeaseForKey(key, lease); err != nil {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, err
	}
	if err := repository.validateProviderStateKey(providerKey); err != nil {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, err
	}
	providerRedisKeys := []string{key.RepositoryKey, key.RepositoryKey + ":lease", key.RepositoryKey + ":fence", providerKey.RepositoryKey}
	for _, conflictKey := range providerConflictKeys {
		if err := repository.validateProviderStateKey(conflictKey); err != nil || conflictKey.RepositoryKey == providerKey.RepositoryKey {
			return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, fmt.Errorf("managed provider state conflict key is invalid")
		}
		providerRedisKeys = append(providerRedisKeys, conflictKey.RepositoryKey)
	}
	if expectedRevision == math.MaxUint64 || consensusPayload.Purpose != ManagedEncryptionPurposeConsensusState || consensusPayload.Revision != expectedRevision+1 ||
		consensusPayload.KeyVersion != key.KeyVersion || providerKey.OwnerHMAC != key.OwnerHMAC || providerPayload.KeyVersion != providerKey.KeyVersion ||
		providerPayload.Purpose != ManagedEncryptionPurposeProviderState || providerPayload.Revision != 1 || strings.TrimSpace(bindingDigest) == "" {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, fmt.Errorf("managed consensus provider-state commit payload is invalid")
	}
	if err := repository.validateTTL(consensusTTL); err != nil {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, err
	}
	if err := repository.validateTTL(providerTTL); err != nil {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, err
	}
	encodedConsensus, err := common.Marshal(consensusPayload)
	if err != nil {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, err
	}
	encodedProvider, err := common.Marshal(providerPayload)
	if err != nil {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, err
	}
	result, err := managedCompareAndSwapWithProviderStateScript.Run(ctx, repository.client,
		providerRedisKeys,
		managedLeaseValue(lease), strconv.FormatUint(expectedRevision, 10), strconv.FormatUint(expectedRevision+1, 10), strconv.FormatUint(lease.FencingToken, 10),
		string(encodedConsensus), consensusTTL.Milliseconds(), repository.fencingCounterRetention.Milliseconds(), bindingDigest, string(encodedProvider), providerTTL.Milliseconds(),
	).Int64()
	if err != nil {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, fmt.Errorf("compare and swap managed consensus with provider state: %w", err)
	}
	switch result {
	case -1:
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, ErrManagedConsensusLeaseInvalid
	case -2:
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, ErrManagedConsensusRevisionConflict
	case -4:
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, ErrProviderStateBindingConflict
	}
	if result <= 0 || uint64(result) != expectedRevision+1 {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, fmt.Errorf("managed consensus provider-state commit returned an invalid revision")
	}
	return ManagedConsensusRecord{Revision: uint64(result), FencingToken: lease.FencingToken, Payload: consensusPayload, ExpiresAt: repository.now().Add(consensusTTL)},
		ManagedProviderStateRecord{BindingDigest: bindingDigest, Payload: providerPayload, ExpiresAt: repository.now().Add(providerTTL)}, nil
}

func (repository *RedisManagedConsensusRepository) CompareAndSwapMigrateConsensus(
	ctx context.Context,
	previousKey ManagedConversationStorageKey,
	activeKey ManagedConversationStorageKey,
	expectedRevision uint64,
	lease ManagedConsensusLease,
	payload ManagedEncryptedEnvelope,
	ttl time.Duration,
) (ManagedConsensusRecord, error) {
	if err := repository.validateConversationKey(previousKey); err != nil {
		return ManagedConsensusRecord{}, err
	}
	if err := repository.validateConversationKey(activeKey); err != nil {
		return ManagedConsensusRecord{}, err
	}
	if previousKey.RepositoryKey == activeKey.RepositoryKey {
		return ManagedConsensusRecord{}, fmt.Errorf("managed consensus migration requires distinct storage keys")
	}
	if err := repository.validateLeaseForKey(previousKey, lease); err != nil {
		return ManagedConsensusRecord{}, err
	}
	if expectedRevision == math.MaxUint64 {
		return ManagedConsensusRecord{}, fmt.Errorf("managed consensus revision overflow")
	}
	if payload.Purpose != ManagedEncryptionPurposeConsensusState || payload.Revision != expectedRevision+1 {
		return ManagedConsensusRecord{}, fmt.Errorf("encrypted consensus payload revision or purpose does not match migration")
	}
	if payload.KeyVersion != activeKey.KeyVersion {
		return ManagedConsensusRecord{}, fmt.Errorf("managed consensus migration payload does not use the active key version")
	}
	if err := repository.validateTTL(ttl); err != nil {
		return ManagedConsensusRecord{}, err
	}
	encodedPayload, err := common.Marshal(payload)
	if err != nil {
		return ManagedConsensusRecord{}, fmt.Errorf("encode encrypted managed consensus migration envelope: %w", err)
	}
	result, err := managedCompareAndSwapMigrateScript.Run(
		ctx,
		repository.client,
		[]string{
			previousKey.RepositoryKey,
			previousKey.RepositoryKey + ":lease",
			previousKey.RepositoryKey + ":fence",
			activeKey.RepositoryKey,
			activeKey.RepositoryKey + ":fence",
			activeKey.RepositoryKey + ":lease",
		},
		managedLeaseValue(lease),
		strconv.FormatUint(expectedRevision, 10),
		strconv.FormatUint(expectedRevision+1, 10),
		strconv.FormatUint(lease.FencingToken, 10),
		string(encodedPayload),
		ttl.Milliseconds(),
		repository.fencingCounterRetention.Milliseconds(),
	).Int64()
	if err != nil {
		return ManagedConsensusRecord{}, fmt.Errorf("migrate managed consensus key version: %w", err)
	}
	switch result {
	case -1:
		return ManagedConsensusRecord{}, ErrManagedConsensusLeaseInvalid
	case -2:
		return ManagedConsensusRecord{}, ErrManagedConsensusRevisionConflict
	case -3:
		return ManagedConsensusRecord{}, ErrManagedConsensusKeyConflict
	}
	if result <= 0 || uint64(result) != expectedRevision+1 {
		return ManagedConsensusRecord{}, fmt.Errorf("managed consensus migration returned an invalid revision")
	}
	return ManagedConsensusRecord{
		Revision:     uint64(result),
		FencingToken: lease.FencingToken,
		Payload:      payload,
		ExpiresAt:    repository.now().Add(ttl),
	}, nil
}

func (repository *RedisManagedConsensusRepository) CompareAndSwapMigrateConsensusWithProviderState(
	ctx context.Context,
	previousKey ManagedConversationStorageKey,
	activeKey ManagedConversationStorageKey,
	expectedRevision uint64,
	lease ManagedConsensusLease,
	consensusPayload ManagedEncryptedEnvelope,
	providerKey ManagedProviderStateStorageKey,
	providerConflictKeys []ManagedProviderStateStorageKey,
	bindingDigest string,
	providerPayload ManagedEncryptedEnvelope,
	consensusTTL time.Duration,
	providerTTL time.Duration,
) (ManagedConsensusRecord, ManagedProviderStateRecord, error) {
	if err := repository.validateConversationKey(previousKey); err != nil {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, err
	}
	if err := repository.validateConversationKey(activeKey); err != nil {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, err
	}
	if err := repository.validateLeaseForKey(previousKey, lease); err != nil {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, err
	}
	if err := repository.validateProviderStateKey(providerKey); err != nil {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, err
	}
	providerRedisKeys := []string{previousKey.RepositoryKey, previousKey.RepositoryKey + ":lease", previousKey.RepositoryKey + ":fence", activeKey.RepositoryKey, activeKey.RepositoryKey + ":fence", activeKey.RepositoryKey + ":lease", providerKey.RepositoryKey}
	for _, conflictKey := range providerConflictKeys {
		if err := repository.validateProviderStateKey(conflictKey); err != nil || conflictKey.RepositoryKey == providerKey.RepositoryKey {
			return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, fmt.Errorf("managed provider state conflict key is invalid")
		}
		providerRedisKeys = append(providerRedisKeys, conflictKey.RepositoryKey)
	}
	if previousKey.RepositoryKey == activeKey.RepositoryKey || expectedRevision == math.MaxUint64 ||
		consensusPayload.Purpose != ManagedEncryptionPurposeConsensusState || consensusPayload.Revision != expectedRevision+1 || consensusPayload.KeyVersion != activeKey.KeyVersion ||
		providerKey.OwnerHMAC != activeKey.OwnerHMAC || providerKey.KeyVersion != activeKey.KeyVersion || providerPayload.KeyVersion != providerKey.KeyVersion ||
		providerPayload.Purpose != ManagedEncryptionPurposeProviderState || providerPayload.Revision != 1 || strings.TrimSpace(bindingDigest) == "" {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, fmt.Errorf("managed consensus provider-state migration payload is invalid")
	}
	if err := repository.validateTTL(consensusTTL); err != nil {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, err
	}
	if err := repository.validateTTL(providerTTL); err != nil {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, err
	}
	encodedConsensus, err := common.Marshal(consensusPayload)
	if err != nil {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, err
	}
	encodedProvider, err := common.Marshal(providerPayload)
	if err != nil {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, err
	}
	result, err := managedCompareAndSwapMigrateWithProviderStateScript.Run(ctx, repository.client,
		providerRedisKeys,
		managedLeaseValue(lease), strconv.FormatUint(expectedRevision, 10), strconv.FormatUint(expectedRevision+1, 10), strconv.FormatUint(lease.FencingToken, 10),
		string(encodedConsensus), consensusTTL.Milliseconds(), repository.fencingCounterRetention.Milliseconds(), bindingDigest, string(encodedProvider), providerTTL.Milliseconds(),
	).Int64()
	if err != nil {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, fmt.Errorf("migrate managed consensus with provider state: %w", err)
	}
	switch result {
	case -1:
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, ErrManagedConsensusLeaseInvalid
	case -2:
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, ErrManagedConsensusRevisionConflict
	case -3:
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, ErrManagedConsensusKeyConflict
	case -4:
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, ErrProviderStateBindingConflict
	}
	if result <= 0 || uint64(result) != expectedRevision+1 {
		return ManagedConsensusRecord{}, ManagedProviderStateRecord{}, fmt.Errorf("managed consensus provider-state migration returned an invalid revision")
	}
	return ManagedConsensusRecord{Revision: uint64(result), FencingToken: lease.FencingToken, Payload: consensusPayload, ExpiresAt: repository.now().Add(consensusTTL)},
		ManagedProviderStateRecord{BindingDigest: bindingDigest, Payload: providerPayload, ExpiresAt: repository.now().Add(providerTTL)}, nil
}

func (repository *RedisManagedConsensusRepository) DeleteConsensus(ctx context.Context, key ManagedConversationStorageKey, expectedRevision uint64, lease ManagedConsensusLease) error {
	if err := repository.validateConversationKey(key); err != nil {
		return err
	}
	if err := repository.validateLeaseForKey(key, lease); err != nil {
		return err
	}
	result, err := managedDeleteConsensusScript.Run(ctx, repository.client, []string{key.RepositoryKey, key.RepositoryKey + ":lease"}, managedLeaseValue(lease), strconv.FormatUint(expectedRevision, 10)).Int64()
	if err != nil {
		return fmt.Errorf("delete managed consensus: %w", err)
	}
	switch result {
	case -1:
		return ErrManagedConsensusLeaseInvalid
	case -2:
		return ErrManagedConsensusRevisionConflict
	case -3:
		return ErrManagedConsensusNotFound
	}
	if result != 1 {
		return fmt.Errorf("delete managed consensus returned an invalid result")
	}
	return nil
}

func (repository *RedisManagedConsensusRepository) LoadProviderStateBinding(ctx context.Context, key ManagedProviderStateStorageKey) (ManagedProviderStateRecord, error) {
	if err := repository.validateProviderStateKey(key); err != nil {
		return ManagedProviderStateRecord{}, err
	}
	fields, err := repository.client.HGetAll(ctx, key.RepositoryKey).Result()
	if err != nil {
		return ManagedProviderStateRecord{}, fmt.Errorf("load provider state binding metadata: %w", err)
	}
	if len(fields) == 0 {
		return ManagedProviderStateRecord{}, ErrProviderStateBindingNotFound
	}
	ttl, err := repository.client.PTTL(ctx, key.RepositoryKey).Result()
	if err != nil {
		return ManagedProviderStateRecord{}, fmt.Errorf("load provider state binding TTL: %w", err)
	}
	if ttl <= 0 {
		return ManagedProviderStateRecord{}, fmt.Errorf("provider state binding has no valid TTL")
	}
	var payload ManagedEncryptedEnvelope
	if err := common.UnmarshalJsonStr(fields[managedRedisPayloadField], &payload); err != nil {
		return ManagedProviderStateRecord{}, fmt.Errorf("decode encrypted provider state binding envelope: %w", err)
	}
	bindingDigest := fields[managedRedisBindingDigestField]
	if bindingDigest == "" {
		return ManagedProviderStateRecord{}, fmt.Errorf("provider state binding digest is missing")
	}
	return ManagedProviderStateRecord{BindingDigest: bindingDigest, Payload: payload, ExpiresAt: repository.now().Add(ttl)}, nil
}

func (repository *RedisManagedConsensusRepository) RegisterProviderStateBinding(ctx context.Context, key ManagedProviderStateStorageKey, bindingDigest string, payload ManagedEncryptedEnvelope, ttl time.Duration) (ManagedProviderStateRecord, error) {
	if err := repository.validateProviderStateKey(key); err != nil {
		return ManagedProviderStateRecord{}, err
	}
	if strings.TrimSpace(bindingDigest) == "" {
		return ManagedProviderStateRecord{}, fmt.Errorf("provider state binding digest is required")
	}
	if payload.Purpose != ManagedEncryptionPurposeProviderState || payload.Revision != 1 || payload.KeyVersion != key.KeyVersion {
		return ManagedProviderStateRecord{}, fmt.Errorf("encrypted provider state payload has invalid revision or purpose")
	}
	if err := repository.validateTTL(ttl); err != nil {
		return ManagedProviderStateRecord{}, err
	}
	encodedPayload, err := common.Marshal(payload)
	if err != nil {
		return ManagedProviderStateRecord{}, fmt.Errorf("encode encrypted provider state binding envelope: %w", err)
	}
	result, err := managedRegisterProviderBindingScript.Run(ctx, repository.client, []string{key.RepositoryKey}, bindingDigest, string(encodedPayload), ttl.Milliseconds()).Int64()
	if err != nil {
		return ManagedProviderStateRecord{}, fmt.Errorf("register provider state binding: %w", err)
	}
	if result == -1 {
		return ManagedProviderStateRecord{}, ErrProviderStateBindingConflict
	}
	if result != 1 {
		return ManagedProviderStateRecord{}, fmt.Errorf("register provider state binding returned an invalid result")
	}
	return ManagedProviderStateRecord{BindingDigest: bindingDigest, Payload: payload, ExpiresAt: repository.now().Add(ttl)}, nil
}

func (repository *RedisManagedConsensusRepository) DeleteProviderStateBinding(ctx context.Context, key ManagedProviderStateStorageKey, expectedBindingDigest string) error {
	if err := repository.validateProviderStateKey(key); err != nil {
		return err
	}
	if strings.TrimSpace(expectedBindingDigest) == "" {
		return fmt.Errorf("expected provider state binding digest is required")
	}
	result, err := managedDeleteProviderBindingScript.Run(ctx, repository.client, []string{key.RepositoryKey}, expectedBindingDigest).Int64()
	if err != nil {
		return fmt.Errorf("delete provider state binding: %w", err)
	}
	switch result {
	case -1:
		return ErrProviderStateBindingConflict
	case -2:
		return ErrProviderStateBindingNotFound
	}
	if result != 1 {
		return fmt.Errorf("delete provider state binding returned an invalid result")
	}
	return nil
}

func (repository *RedisManagedConsensusRepository) validateConversationKey(key ManagedConversationStorageKey) error {
	if repository == nil || repository.client == nil {
		return fmt.Errorf("managed consensus Redis repository is unavailable")
	}
	if !strings.HasPrefix(key.RepositoryKey, managedConsensusRepositoryKeyPrefix+":") || key.OwnerHMAC == "" || key.ConversationHMAC == "" || key.KeyVersion == "" {
		return fmt.Errorf("managed consensus conversation storage key is invalid")
	}
	return nil
}

func (repository *RedisManagedConsensusRepository) validateProviderStateKey(key ManagedProviderStateStorageKey) error {
	if repository == nil || repository.client == nil {
		return fmt.Errorf("managed consensus Redis repository is unavailable")
	}
	if !strings.HasPrefix(key.RepositoryKey, managedProviderStateRepositoryPrefix+":") || key.OwnerHMAC == "" || key.StateReferenceHMAC == "" || key.KeyVersion == "" {
		return fmt.Errorf("managed provider state storage key is invalid")
	}
	return nil
}

func (repository *RedisManagedConsensusRepository) validateLease(lease ManagedConsensusLease) error {
	if repository == nil || repository.client == nil {
		return fmt.Errorf("managed consensus Redis repository is unavailable")
	}
	if !strings.HasPrefix(lease.RepositoryKey, managedConsensusRepositoryKeyPrefix+":") || strings.TrimSpace(lease.HolderID) == "" || lease.FencingToken == 0 {
		return ErrManagedConsensusLeaseInvalid
	}
	return nil
}

func (repository *RedisManagedConsensusRepository) validateLeaseForKey(key ManagedConversationStorageKey, lease ManagedConsensusLease) error {
	if err := repository.validateLease(lease); err != nil {
		return err
	}
	if lease.RepositoryKey != key.RepositoryKey {
		return ErrManagedConsensusLeaseInvalid
	}
	return nil
}

func (repository *RedisManagedConsensusRepository) validateTTL(ttl time.Duration) error {
	if repository == nil || repository.maximumRetention <= 0 {
		return fmt.Errorf("managed consensus Redis repository is unavailable")
	}
	if ttl <= 0 || ttl > repository.maximumRetention || ttl.Milliseconds() <= 0 {
		return fmt.Errorf("managed consensus TTL must be positive and within maximum retention")
	}
	return nil
}

func managedLeaseValue(lease ManagedConsensusLease) string {
	return lease.HolderID + ":" + strconv.FormatUint(lease.FencingToken, 10)
}

var _ ManagedConsensusRepository = (*RedisManagedConsensusRepository)(nil)
var _ ManagedConsensusKeyRotationRepository = (*RedisManagedConsensusRepository)(nil)
var _ ManagedConsensusProviderStateRepository = (*RedisManagedConsensusRepository)(nil)
var _ ManagedConsensusProviderStateKeyRotationRepository = (*RedisManagedConsensusRepository)(nil)
