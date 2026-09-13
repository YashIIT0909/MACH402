// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/**
 * IdentityRegistry — ERC-8004 style identity for MACH402 providers.
 *
 * A provider registers once and gets a persistent `agentId`. That id answers
 * "who is this provider", and nothing else: GPU model, price and availability
 * change every thirty seconds and live in the MACH402 registry, not here.
 * Putting them on-chain would mean a transaction per heartbeat for information
 * that is stale by the time it confirms.
 *
 * What the id buys is a claim our own registry cannot forge. Registration is
 * bound to `msg.sender`, so MACH402 cannot mint an identity for a provider,
 * transfer one, or revoke one — which is the only reason it is worth being
 * on-chain rather than another column in our Postgres.
 *
 * `agentDomain` is the host of the node's `public_url`, and the node serves
 * `GET /.well-known/agent-card.json` there. So the resolution path is
 * on-chain id -> domain -> a card the provider serves themselves, with our
 * registry nowhere in it.
 *
 * There is no owner and no upgrade path here either, for the same reason as
 * SessionEscrow: a privileged party who could rewrite identities would defeat
 * the point of recording them.
 */
contract IdentityRegistry {
    struct AgentInfo {
        uint256 agentId;
        string  agentDomain;
        address agentAddress;
    }

    /** Ids start at 1, so 0 is a usable "not registered" sentinel in config. */
    uint256 private _nextAgentId = 1;

    mapping(uint256 => AgentInfo) private _agents;
    mapping(bytes32 => uint256) private _idByDomainHash;
    mapping(address => uint256) private _idByAddress;

    event AgentRegistered(uint256 indexed agentId, string agentDomain, address indexed agentAddress);
    event AgentUpdated(uint256 indexed agentId, string agentDomain, address indexed agentAddress);

    error NotAgentAddress();
    error EmptyDomain();
    error DomainTaken();
    error AddressTaken();
    error NoSuchAgent();
    error NotAgentOwner();

    /**
     * Registers a new agent. `agentAddress` must be the caller, so a provider
     * can only ever register themselves — nobody can squat an identity for
     * someone else's machine, and we cannot mint one on anyone's behalf.
     */
    function newAgent(string calldata agentDomain, address agentAddress)
        external
        returns (uint256 agentId)
    {
        if (agentAddress != msg.sender) revert NotAgentAddress();
        if (bytes(agentDomain).length == 0) revert EmptyDomain();

        bytes32 domainHash = keccak256(bytes(agentDomain));
        if (_idByDomainHash[domainHash] != 0) revert DomainTaken();
        if (_idByAddress[agentAddress] != 0) revert AddressTaken();

        agentId = _nextAgentId++;
        _agents[agentId] = AgentInfo(agentId, agentDomain, agentAddress);
        _idByDomainHash[domainHash] = agentId;
        _idByAddress[agentAddress] = agentId;

        emit AgentRegistered(agentId, agentDomain, agentAddress);
    }

    /**
     * Moves an existing agent to a new domain and/or key. A provider who
     * re-homes their node or rotates their operator key keeps their identity
     * and their history rather than starting over as a stranger.
     *
     * Only the agent's current address may do this.
     */
    function updateAgent(uint256 agentId, string calldata newDomain, address newAddress)
        external
        returns (bool)
    {
        AgentInfo storage a = _agents[agentId];
        if (a.agentId == 0) revert NoSuchAgent();
        if (a.agentAddress != msg.sender) revert NotAgentOwner();
        if (bytes(newDomain).length == 0) revert EmptyDomain();
        if (newAddress == address(0)) revert NotAgentAddress();

        bytes32 oldHash = keccak256(bytes(a.agentDomain));
        bytes32 newHash = keccak256(bytes(newDomain));

        if (newHash != oldHash) {
            if (_idByDomainHash[newHash] != 0) revert DomainTaken();
            delete _idByDomainHash[oldHash];
            _idByDomainHash[newHash] = agentId;
            a.agentDomain = newDomain;
        }

        if (newAddress != a.agentAddress) {
            if (_idByAddress[newAddress] != 0) revert AddressTaken();
            delete _idByAddress[a.agentAddress];
            _idByAddress[newAddress] = agentId;
            a.agentAddress = newAddress;
        }

        emit AgentUpdated(agentId, a.agentDomain, a.agentAddress);
        return true;
    }

    function getAgent(uint256 agentId) external view returns (AgentInfo memory) {
        AgentInfo memory a = _agents[agentId];
        if (a.agentId == 0) revert NoSuchAgent();
        return a;
    }

    function resolveByDomain(string calldata agentDomain) external view returns (AgentInfo memory) {
        uint256 id = _idByDomainHash[keccak256(bytes(agentDomain))];
        if (id == 0) revert NoSuchAgent();
        return _agents[id];
    }

    function resolveByAddress(address agentAddress) external view returns (AgentInfo memory) {
        uint256 id = _idByAddress[agentAddress];
        if (id == 0) revert NoSuchAgent();
        return _agents[id];
    }

    /**
     * Non-reverting lookup, for `cleargate-node register` to ask "do I already
     * have an identity?" without treating a plain "no" as an error. Registering
     * twice would orphan the first id, so this check is what makes the
     * register command safely re-runnable.
     */
    function agentIdOf(address agentAddress) external view returns (uint256) {
        return _idByAddress[agentAddress];
    }

    /** Total registered agents. Also the next id minus one. */
    function agentCount() external view returns (uint256) {
        return _nextAgentId - 1;
    }
}
