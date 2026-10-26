// Package mech holds the mechanism ids and their ablation switches (SPEC §2,
// rule 3). A mechanism runs unless NANOBIRD_ABLATION or the config's
// mechanisms.disable names it. Ids the code does not implement yet are refused,
// so a leave-one-out run never silently measures nothing.
package mech
