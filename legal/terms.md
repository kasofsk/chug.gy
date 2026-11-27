# Terms of Service

Chuggy, Inc.

Effective [date to be set]

Chuggy is an orchestrator for coding agents. You file tickets, agents do the work on machines you run under your own model accounts, and Chuggy keeps the record of what ran and lands the result in your repository.

These terms are an agreement between you and Chuggy, Inc. (“Chuggy”, “we”). They cover chug.gy, the hosted Chuggy service, and the runner software and other tools we provide for use with it (together, the “Service”). By creating an account or using the Service in any way, including through an access token, a runner or a connected tool, you agree to these terms. If you do not agree, do not use the Service.

If you use the Service for a company or other organization, you agree to these terms for that organization and confirm that you are allowed to. “You” then means both you and the organization.

These terms do not cover the Chuggy service when you or your organization run it yourselves, or work we do for you under a separate written agreement.

## The short version

- You must be 18 or older.
- Your content, and what your agents produce, stays yours. We use it only to run, secure and support the Service, and as the law requires.
- Agents act on their own, on your machines and in your repositories. You decide what they can reach and what lands, and you are responsible for the results.
- Chuggy does not supply AI models. You bring your own model accounts and follow their providers’ terms.
- The Service is free today. It is new, it comes without warranties, and our liability is limited.
- When we change these terms, we ask you to agree again. If you keep using the Service, the new terms apply after 30 days.

This summary is here to help. The sections below are the terms.

## 1. Who can use the Service

You must be at least 18 years old and able to enter into a contract. You may not use the Service if the laws of the United States or of the place where you live bar you from doing so, including sanctions and export-control laws.

## 2. Your account and workspace

**Your account.** You sign in with your GitHub account. You are responsible for everything done under your account, including through the access tokens you create and the runners and chat hosts you register. In these terms, “runners” includes chat hosts. Do not let anyone else use your account. Keep your tokens and your GitHub account secure, and tell us promptly if you think someone else has used them.

**Workspaces.** A workspace’s admins, and the admins of its projects, decide who its members are and what each may do, and they are responsible for the members they add. If you join a workspace that someone else administers, its admins control the workspace and its content and can remove you, and the members of a project can see the work you do in it. The work you did there stays with the workspace if you leave.

## 3. How the Service works, and what you are responsible for

The Service coordinates work that happens on your machines, in your repositories and under your accounts. This section says which parts are yours.

**Your machines.** The runner software runs on machines you choose. There it runs coding agents, which execute commands, read and change files and use the network, with whatever access the machine and your configuration give them. You are responsible for those machines: where the runner is installed, what it can reach, and keeping it secure. By default the runner updates itself to each release we name for it, and you can turn that off in its settings. It also downloads the runtime that jobs run with, which includes software made by others. By installing the runner, you allow it to download that software to your machine and run it there.

**Agents act on their own.** A coding agent decides its own steps. The Service sends it your ticket and records what it does. Neither we nor the Service reviews or approves each action before the agent takes it. You are responsible for what agents do under your account, on your machines and with your credentials.

**Your repositories.** The Service acts in the repositories you connect, through the GitHub Apps you install and with the permissions you grant. It creates branches, commits and pull requests, and, when the conditions set by your project’s configuration and the ticket are met, it pushes changes to a branch or merges them. Depending on those settings, a change can land without a person reviewing it. You confirm that you have the right to connect each repository, and you are responsible for your configuration, your tickets and what lands under them.

**Your model accounts and other services.** Chuggy does not provide AI models. Agents run under accounts you hold with a model provider, using agent software, such as Claude Code or Codex, that its maker licenses to you. GitHub, your model provider and the makers of that software are separate from us. Your agreements with them govern your use of their products, and you are responsible for following those agreements and for anything they charge you. Use only accounts and credentials that you are authorized to use, and do not use the Service to share or resell access to an account in a way its provider does not allow. We are not responsible for those services.

**What agents produce.** AI models make mistakes. What an agent writes can be wrong, incomplete or insecure, and it can resemble existing work, including work that others own. The checks and reviews you run through the Service reduce those risks and do not remove them. You are responsible for deciding whether output is fit to use, to merge and to ship.

**What agents read.** The record of an agent’s work includes the contents of files it read and the output of commands it ran, and that record is stored on our servers. Keep secrets out of your agents’ reach. The Service is not designed for information that carries special legal protections, such as health records or payment card numbers, so do not expose that information to your agents or put it in your tickets.

## 4. Your content

**It is yours.** “Your content” means what you and your agents put into the Service or produce through it: tickets, messages, images, configuration, code and the record of agent work. As between you and Chuggy, and to the extent the law allows, you own your content, including what your agents produce. We claim no ownership of it.

**What you allow us to do with it.** You give Chuggy a worldwide, non-exclusive, royalty-free license to host, copy, transmit, display and otherwise process your content, only as needed to provide, secure, support and troubleshoot the Service, to enforce these terms, to protect the rights and safety of our users, the public and Chuggy, and to meet our legal obligations, in the ways our Privacy Policy describes. That includes showing it to the people your workspace’s settings allow, sending it to the services you connect, and having our service providers process it for us. We may also compile and publish statistics about how the Service is used that do not identify you or your workspace and do not reveal your content.

**What you promise about it.** You have the rights you need to put your content into the Service and to give us this license, and neither your content nor your use of the Service breaks the law or violates anyone else’s rights.

**Copyright complaints.** If you believe that content on the Service infringes your copyright, send a notice to our designated copyright agent at legal@chug.gy or at the address in section 17, with the information that 17 U.S.C. § 512(c)(3) requires. We act on notices that meet it, and we end the accounts of people who infringe repeatedly.

## 5. Acceptable use

You will not, and will not direct an agent to:

- break the law, or infringe or violate anyone’s rights;
- create or spread malware, or attack, probe or gain access to any system, account or data without authorization;
- interfere with or disrupt the Service, get around its limits or access controls, or test its security without our written permission;
- access another workspace, or another person’s content, without permission;
- use the Service to send spam, or to deceive or harass anyone;
- misrepresent who you are, or create accounts in bulk or for anyone but yourself;
- resell the Service, or offer it to others as your own, without a written agreement with us.

We may investigate suspected violations and remove content that breaks these terms, and we may suspend or end access as section 10 describes.

## 6. Our software and the Service

**The Service.** While you follow these terms, you may use the Service for your own work and your organization’s. Chuggy and its licensors own the Service and the Chuggy name and logo. These terms give you no rights in them beyond the right to use the Service.

**Software we publish.** The runner software and some of the other tools we publish come with an open-source license. That license governs your rights to copy, change and share the software, and it governs where it conflicts with these terms. The runtime that jobs run with includes software made by others, such as Claude Code and Codex, which comes under its maker’s license. Where a piece of our software comes with no license, you may install and run it only to use the Service.

**Feedback.** If you send us ideas or suggestions about the Service, we may use them freely and without any obligation to you.

## 7. Changes and availability

The Service is new and still changing. We may add, change or remove features at any time. We do not promise that the Service will always be available, or that it will keep everything you put into it, so keep your own copies of anything you need. If we decide to shut the Service down, we will give you at least 30 days’ notice.

## 8. Fees

The Service is free today. If we introduce fees, we will tell you at least 30 days before they apply, and you will not be charged unless you agree to pay. Work we do for you under a separate written agreement, such as helping you run the Chuggy service in your own infrastructure, is paid for under that agreement.

## 9. Privacy

Our [Privacy Policy](https://chug.gy/privacy) explains what information we collect through the Service, including what your runners send us, how we use it and the choices you have.

Where we process personal data in your content for you, we do so only on your instructions, which are these terms and your use of the Service, and we use the service providers our Privacy Policy names, which you authorize. If the law requires more, write to us and we will sign a data processing agreement.

## 10. Suspension and ending

**By you.** You can stop using the Service at any time. To have your account deleted, or a project or workspace that you administer, ask us as the Privacy Policy describes.

**By us.** We may suspend or end your access if you break these terms, if your use puts the Service or anyone else at risk, or if the law requires it. Where we reasonably can, we will tell you first and give you the chance to fix the problem. We may also end your access for any other reason with at least 30 days’ notice, and during that time you can still read and copy your content.

**Afterwards.** When your access ends, your right to use the Service ends with it, and we may delete your content, except work that stays with a workspace under section 2. Sections 2, 3, 4 and 6 (apart from the right to use the Service), this section and sections 11 through 16 continue to apply.

## 11. No warranties

**The Service, the software we publish and everything else we provide are provided “as is” and “as available”. To the fullest extent the law allows, Chuggy makes no warranties of any kind, whether express, implied or statutory, and disclaims the implied warranties of merchantability, fitness for a particular purpose, title and non-infringement.**

**In particular, we do not warrant that the Service will be uninterrupted, secure or free of errors; that your content will be kept or will not be lost; or that anything an agent does or produces will be correct, safe, original or fit for your purpose. Nothing we say on our website, in our documentation or anywhere else creates a warranty that these terms do not state.**

**The Service is not designed for uses in which a failure or a mistake could lead to death, injury, or serious damage to property or the environment. Do not use it for them.**

## 12. Limits on liability

**To the fullest extent the law allows:**

**(a) Chuggy and its officers, directors, employees and contractors will not be liable for any indirect, incidental, special, consequential, exemplary or punitive damages, or for any loss of profits, revenue, business, goodwill or data, or for the cost of substitute services, arising out of or relating to the Service or these terms.**

**(b) The total liability of Chuggy and those people for all claims arising out of or relating to the Service or these terms will not be more than the greater of US$100 and the amount you paid us for the Service in the 12 months before the first event that gave rise to the liability.**

**These limits apply to every kind of claim, whether in contract, tort (including negligence), strict liability or any other theory, even if we have been told that the damage was possible, and even if a remedy fails of its essential purpose. They cover, among other things, anything an agent does on your machines, in your repositories or with your accounts, and anything a third party charges you.**

**Some laws do not allow some of these limits. Where that is so, the limits apply only as far as the law allows. Nothing in these terms limits liability for death or personal injury caused by negligence, for fraud, or for anything else the law does not allow to be limited.**

## 13. Claims against us

To the extent the law allows, you will defend Chuggy and its officers, directors, employees and contractors against any claim by a third party, and pay the damages, costs and reasonable legal fees that result, where the claim alleges that your content infringes someone’s rights or breaks the law, or arises from your use of the Service in breach of these terms or of the law, or from your breach of an agreement with someone else, such as GitHub or your model provider. This section does not cover a claim to the extent we caused it. We will tell you promptly about the claim and give you control of its defense. We may take part with our own lawyers at our own cost, and you will not settle the claim in a way that puts an obligation on us without our written consent.

## 14. Disputes

**Governing law.** The laws of the State of Delaware govern these terms and any dispute about them or the Service, without regard to its conflict-of-law rules.

**Talk to us first.** Before either of us starts a legal proceeding, that party will send the other a written description of the dispute, and we will both try in good faith for 30 days to resolve it. Any time limit for bringing the claim pauses for those 30 days. Neither of us has to wait before asking a court for an urgent order to stop misuse of its intellectual property or unauthorized access to its systems.

**Courts.** A dispute that is not resolved that way will be decided only in the state or federal courts located in Delaware, and each of us accepts the jurisdiction of those courts. An urgent order of the kind described above may be sought in any court with jurisdiction.

**No jury and no class actions. To the extent the law allows, each of us gives up the right to a jury trial, and each of us will bring claims only on our own behalf and not as part of a class or representative action.**

**Consumers.** If you are a consumer in a place whose law gives you protections that a contract cannot take away, including the right to bring a claim in the courts where you live, nothing in these terms takes them away.

## 15. Changes to these terms

We may change these terms. When we do, we will post the new version at chug.gy/terms with its effective date, and the Chuggy console will ask you to agree to it the next time you open it. The new version applies to you when you agree to it, or 30 days after we post it if you are still using the Service then, including through an access token or a runner, whichever comes first. A change does not apply to a dispute that arose before we posted it. If you do not agree, stop using the Service, including your runners and access tokens; you can ask us to delete your account. Each earlier version stays available at its own address.

## 16. General

**Whole agreement.** These terms, and the licenses that section 6 refers to, are the whole agreement between you and Chuggy about the Service, and they replace any earlier understanding about it. If you and we have signed a separate written agreement, such as one for support, consulting or data processing, that agreement governs where it conflicts with these terms.

**If part is unenforceable.** If a court finds any part of these terms unenforceable, that part will be enforced as far as the law allows and the rest will stay in effect.

**No waiver.** If we do not enforce a term, we have not given it up.

**Transfer.** You may not transfer these terms or your account to anyone else without our written consent. We may transfer these terms to an affiliate, or to a successor in a merger, acquisition, reorganization or sale of our assets.

**Notices.** We may send you notices in the Service or to the email address on your account. Send notices to us at legal@chug.gy or at the postal address in section 17.

**Events outside our control.** We are not responsible for failing to do something, or for doing it late, because of events beyond our reasonable control.

**Export.** You will follow the export-control and sanctions laws that apply to your use of the Service.

**Relationship.** These terms do not make you and Chuggy partners, agents or employees of each other. They give no rights to anyone who is not a party to them, except that the people named in sections 12 and 13 may rely on those sections.

## 17. Contact

Chuggy, Inc., c/o Legalinc Corporate Services Inc., 131 Continental Dr, Suite 305, Newark, DE 19713, USA

legal@chug.gy
